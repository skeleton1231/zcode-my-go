package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/winpath"
)

// Bash timeouts.
const (
	bashDefaultTimeout = 120 * time.Second
	bashMaxTimeout     = 600 * time.Second
)

// Bash runs a shell command in the workspace. Windows uses PowerShell; Unix
// uses bash (falling back to sh).
type Bash struct {
	ws   Workspace
	jobs *Jobs

	// cwd 是上次 cd 后的启动目录（jail 内绝对路径；空 = 工作区根，
	// issue #221）。环境变量不持久——只粘滞目录，降低副作用面。并
	// 行批次内共享起点、各自收尾 last-write 更新（对齐同路径串行化
	// 调度的近似）。
	cwdMu sync.Mutex
	cwd   string

	// lastOut 是最近一次前台命令的结果文本（测试断言便利）。
	lastOut string
}

// cwdMarker 是命令尾部追加的目录哨兵：shell 回显最终目录，工具侧解
// 析并剥离——不泄漏进模型可见输出（issue #221）。
const cwdMarker = "__ZGO_CWD__:"

// dirFor 返回本条命令的启动目录（持锁读快照）。
func (b *Bash) dirFor() string {
	b.cwdMu.Lock()
	defer b.cwdMu.Unlock()
	if b.cwd == "" {
		return b.ws.Root
	}
	return b.cwd
}

// wrapCwd 给命令追加目录哨兵（平台 shell 各自形态）。
func wrapCwd(line string) string {
	if runtime.GOOS == "windows" {
		return line + "; Write-Output \"" + cwdMarker + "$PWD\""
	}
	return line + "; printf '\\n" + cwdMarker + "%s\\n' \"$PWD\""
}

// absorbCwd 从输出里解析哨兵：剥离标记行、更新粘滞目录（越出工作区
// 根的不持久——jail 语义）。返回净化后的输出。
func (b *Bash) absorbCwd(out string) string {
	i := strings.LastIndex(out, cwdMarker)
	if i < 0 {
		return out
	}
	rest := out[i+len(cwdMarker):]
	end := strings.IndexAny(rest, "\r\n")
	dir := rest
	if end >= 0 {
		dir = rest[:end]
	}
	dir = strings.TrimSpace(dir)
	// 剥离标记行（含前导换行）。
	cut := i
	if cut > 0 && (out[cut-1] == '\n') {
		cut--
		if cut > 0 && out[cut-1] == '\r' {
			cut--
		}
	}
	tail := ""
	if end >= 0 {
		tail = rest[end:]
	}
	cleaned := out[:cut] + strings.TrimLeft(tail, "\r\n")
	if dir != "" && b.underRoot(dir) {
		b.cwdMu.Lock()
		b.cwd = dir
		b.cwdMu.Unlock()
	}
	return cleaned
}

// underRoot 报告 dir 是否在工作区根内（大小写与 8.3 短名归一后）。
// 附加工作目录（spec 规则 128）同权：cd 可以进入任一根。
func (b *Bash) underRoot(dir string) bool {
	if b.ws.IsRemote() {
		root := strings.TrimSuffix(b.ws.Root, "/")
		return strings.HasPrefix(strings.ToLower(strings.TrimSuffix(dir, "/")), strings.ToLower(root+"/")) || strings.EqualFold(strings.TrimSuffix(dir, "/"), root)
	}
	roots := []string{b.ws.Root}
	roots = append(roots, b.ws.Extra...)
	for _, root := range roots {
		r, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(winpath.Long(r), winpath.Long(abs))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// NewBash builds the bash tool.
func NewBash(ws Workspace, jobs *Jobs) *Bash { return &Bash{ws: ws, jobs: jobs} }

// Name implements agent.Tool.
func (b *Bash) Name() string { return "bash" }

// Description implements agent.Tool.
func (b *Bash) Description() string {
	return "Run a shell command in the workspace directory. On Windows the " +
		"command runs in PowerShell; elsewhere in bash. stdout and stderr are " +
		"combined, output is truncated to 30KB. The working directory is " +
		"sticky: cd persists across calls (staying inside the workspace). " +
		"timeout_ms caps the run " +
		"(default 120000, max 600000). background:true launches without " +
		"waiting (dev servers, watchers) — drain output with bash_output, " +
		"stop with bash_kill; local and SSH workspaces behave the same."
}

// Parameters implements agent.Tool.
func (b *Bash) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"command":    prop("string", "The command line to run"),
		"timeout_ms": prop("number", "Timeout in milliseconds (default 120000, max 600000)"),
		"cwd":        prop("string", "Working directory for this run (workspace-relative; must stay inside the workspace)"),
		"background": prop("boolean", "Launch without waiting for completion; returns a job id for bash_output/bash_kill"),
	}, "command")
}

// Execute implements agent.Tool.
func (b *Bash) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Command    string `json:"command"`
		TimeoutMS  int    `json:"timeout_ms"`
		Cwd        string `json:"cwd"`
		Background bool   `json:"background"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.Command == "" {
		return errResult("command is empty")
	}
	// cwd 参数（spec 规则 110）：本次运行的起始目录——path jail 限界
	//（越界即错误结果）；覆盖粘滞 cwd 一次，不污染后续命令的粘滞态。
	runDir := ""
	if a.Cwd != "" {
		abs, err := b.ws.Resolve(a.Cwd)
		if err != nil {
			return errResult("cwd: %v", err)
		}
		runDir = abs
	}
	// 浏览器（wasm）目标没有进程模型：给出明确错误而不是让 exec 在
	// 运行时报 "not supported"，模型可见结果自纠。
	if runtime.GOOS == "js" {
		return errResult("bash 工具在浏览器目标不可用（wasm 无进程模型），请改用文件工具")
	}
	if a.Background {
		return b.startBackground(a.Command)
	}
	timeout := bashDefaultTimeout
	if a.TimeoutMS > 0 {
		timeout = time.Duration(a.TimeoutMS) * time.Millisecond
		if timeout > bashMaxTimeout {
			timeout = bashMaxTimeout
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if b.ws.IsRemote() {
		command := a.Command
		if runDir != "" {
			// 远端 Exec 在会话根执行：abs 转根相对斜杠形态；单引号
			// 封闭注入面（与远端快照恢复同约定）。失败 cd . 兜底进根。
			rel, err := filepath.Rel(b.ws.Root, runDir)
			if err != nil {
				return errResult("cwd: %v", err)
			}
			command = "cd '" + filepath.ToSlash(rel) + "' 2>/dev/null || cd .; " + command
		}
		return b.execRemote(ctx, command)
	}

	// cwd 粘滞（issue #221）：从上次 cd 的目录启动，命令尾部追加目录
	// 哨兵（输出侧解析剥离，不泄漏）。
	cmd, err := b.command(ctx, wrapCwd(a.Command))
	if err != nil {
		return errResult("%v", err)
	}
	if runDir != "" {
		cmd.Dir = runDir
	} else {
		cmd.Dir = b.dirFor()
	}
	// 击杀必须覆盖整棵进程树（issue #7）：exec 的默认 kill 只杀直接
	// 子进程，Start-Process 派生的孙进程会活过超时/中止。平台差异
	// 在 prockill_*.go。
	setupTreeKill(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// 分类必须先看 ctx.Err()（issue #6）：击杀后 Windows 上 Run 返回
		// *exec.ExitError（被杀进程 exit code 1），按 err 类型分类会把
		// 超时/中止误报成 exit status 1，丢失超时信息与 partial output。
		if ctxErr := ctx.Err(); ctxErr != nil {
			partial := truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail)
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return errResult("timed out after %s\npartial output:\n%s", timeout, partial)
			}
			return agent.ToolResult{IsError: true,
				Content: "canceled\npartial output:\n" + partial}
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return agent.ToolResult{
				IsError: true,
				Content: fmt.Sprintf("exit status %d\n%s",
					exitErr.ExitCode(), truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail)),
			}
		}
		return errResult("running command: %v\n%s", err,
			truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail))
	}
	cleaned := b.absorbCwd(out.String())
	b.lastOut = cleaned
	return agent.ToolResult{Content: truncateBytes([]byte(cleaned), bashHeadTail, bashHeadTail)}
}

// command builds the platform shell invocation.
func (b *Bash) command(ctx context.Context, line string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell.exe",
			"-NoProfile", "-NonInteractive", "-Command", line), nil
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return exec.CommandContext(ctx, "bash", "-c", line), nil
	}
	if _, err := exec.LookPath("sh"); err == nil {
		return exec.CommandContext(ctx, "sh", "-c", line), nil
	}
	return nil, fmt.Errorf("no shell found")
}

// Capability implements agent.Capabler: shell commands run anything.
func (b *Bash) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// CapabilityForArgs implements agent.ArgsCapabler: inspection commands
// (git status, ls, grep…) are read-only and auto-allowed; everything else
// keeps the medium-risk default (ZCode's bash readonly policy).
func (b *Bash) CapabilityForArgs(args string) permission.Capability {
	var a struct {
		Command string `json:"command"`
	}
	cap := b.Capability()
	if json.Unmarshal([]byte(args), &a) == nil && BashCommandReadonly(a.Command) {
		cap.ReadOnly = true
		cap.Risk = permission.RiskLow
	}
	return cap
}

// startBackground launches a long command without blocking the turn
// (ZCode's bash background): output drains via bash_output, the tree
// stops via bash_kill or when the session ends. 本地与 SSH 远程同构——
// 远端经 RemoteFS.StartBackground 起一条不等待的 exec 通道。
func (b *Bash) startBackground(line string) agent.ToolResult {
	if b.jobs == nil {
		return errResult("no job registry in this session")
	}
	proc, err := b.bgProcess(line)
	if err != nil {
		return errResult("%v", err)
	}
	job, err := b.jobs.Start(proc, line)
	if err != nil {
		return errResult("starting job: %v", err)
	}
	go io.Copy(jobWriter{job}, proc.Stdout())
	return okResult("后台任务已启动 [%s]：%s\n用 bash_output 读取输出，bash_kill 停止。", job.ID, line)
}

// bgProcess 构建后台进程：远程走 SSH 通道，本地走平台 shell。
func (b *Bash) bgProcess(line string) (BgProcess, error) {
	if b.ws.IsRemote() {
		return b.ws.Remote.StartBackground(line)
	}
	cmd, err := b.command(context.Background(), line)
	if err != nil {
		return nil, err
	}
	setupTreeKill(cmd)
	cmd.Dir = b.ws.Root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // 合并到同一管道，输出顺序即产出顺序
	return localBgProcess{cmd: cmd, out: stdout}, nil
}

// localBgProcess adapts *exec.Cmd + its stdout pipe to BgProcess.
type localBgProcess struct {
	cmd *exec.Cmd
	out io.ReadCloser
}

func (l localBgProcess) Start() error          { return l.cmd.Start() }
func (l localBgProcess) KillTree() error       { return killTree(l.cmd) }
func (l localBgProcess) Wait() error           { return l.cmd.Wait() }
func (l localBgProcess) Stdout() io.ReadCloser { return l.out }

// jobWriter feeds a job's output buffer from an io.Copy.
type jobWriter struct{ job *Job }

func (w jobWriter) Write(p []byte) (int, error) {
	w.job.append(p)
	return len(p), nil
}

// execRemote runs the command on the SSH workspace (the test bed serves
// the same shell semantics as a real host's default shell).
func (b *Bash) execRemote(ctx context.Context, command string) agent.ToolResult {
	type execResult interface {
		Exec(cmd string) (ExecOutcome, error)
	}
	res, err := b.ws.Remote.Exec(command)
	if err != nil {
		if ctx.Err() != nil {
			return agent.ToolResult{IsError: true, Content: "canceled"}
		}
		return errResult("running remote command: %v", err)
	}
	if res.ExitCode != 0 {
		return agent.ToolResult{IsError: true,
			Content: fmt.Sprintf("exit status %d\n%s", res.ExitCode,
				truncateBytes([]byte(res.Output), bashHeadTail, bashHeadTail))}
	}
	return agent.ToolResult{Content: truncateBytes([]byte(res.Output), bashHeadTail, bashHeadTail)}
}
