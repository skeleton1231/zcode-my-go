package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
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
}

// NewBash builds the bash tool.
func NewBash(ws Workspace, jobs *Jobs) *Bash { return &Bash{ws: ws, jobs: jobs} }

// Name implements agent.Tool.
func (b *Bash) Name() string { return "bash" }

// Description implements agent.Tool.
func (b *Bash) Description() string {
	return "Run a shell command in the workspace directory. On Windows the " +
		"command runs in PowerShell; elsewhere in bash. stdout and stderr are " +
		"combined, output is truncated to 30KB. timeout_ms caps the run " +
		"(default 120000, max 600000)."
}

// Parameters implements agent.Tool.
func (b *Bash) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"command":    prop("string", "The command line to run"),
		"timeout_ms": prop("number", "Timeout in milliseconds (default 120000, max 600000)"),
	}, "command")
}

// Execute implements agent.Tool.
func (b *Bash) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Command    string `json:"command"`
		TimeoutMS  int    `json:"timeout_ms"`
		Background bool   `json:"background"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.Command == "" {
		return errResult("command is empty")
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
		return b.execRemote(ctx, a.Command)
	}

	cmd, err := b.command(ctx, a.Command)
	if err != nil {
		return errResult("%v", err)
	}
	cmd.Dir = b.ws.Root
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
	return agent.ToolResult{Content: truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail)}
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
// stops via bash_kill or when the session ends.
func (b *Bash) startBackground(line string) agent.ToolResult {
	if b.jobs == nil {
		return errResult("no job registry in this session")
	}
	cmd, err := b.command(context.Background(), line)
	if err != nil {
		return errResult("%v", err)
	}
	setupTreeKill(cmd)
	cmd.Dir = b.ws.Root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errResult("%v", err)
	}
	cmd.Stderr = cmd.Stdout // 合并到同一管道，输出顺序即产出顺序

	job, err := b.jobs.Start(execProcess{cmd}, line)
	if err != nil {
		return errResult("starting job: %v", err)
	}
	go io.Copy(jobWriter{job}, stdout)
	return okResult("后台任务已启动 [%s]：%s\n用 bash_output 读取输出，bash_kill 停止。", job.ID, line)
}

// execProcess adapts *exec.Cmd to the job interface.
type execProcess struct{ cmd *exec.Cmd }

func (e execProcess) Start() error    { return e.cmd.Start() }
func (e execProcess) KillTree() error { return killTree(e.cmd) }
func (e execProcess) Wait() error     { return e.cmd.Wait() }

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
