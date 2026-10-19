package lsp

// gopls 常驻 daemon（LSP 路线 A v2，spec 规则 69）：v1 每次查询 spawn
// 一个 gopls 子进程（definition 冷启 1-3s——载包开销重复付）；v2 每个
// 工作区根一个常驻进程走 JSON-RPC，initialize 只付一次。策略：
//   - 位置编码协商 utf-8（与 v1 CLI 的字节列一致）；服务端不认则该
//     daemon 标记不支持，全部调用退回 CLI 子进程路径；
//   - 进程死亡/连接断：同根重启一次重试一次，再失败透传错误；
//   - 空闲 10 分钟回收；服务 Close 时全量收割（CloseAll）；
//   - 编辑回灌路径先 NotifyChanged（didChangeWatchedFiles）再拉诊断，
//     保证磁盘改动即刻可见（不依赖 gopls 的文件监视器）。
// 工作区级诊断（check ./...）无 LSP 等价物，留在 CLI 路径。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	daemonIdleTimeout = 10 * time.Minute
	daemonInitTimeout = 20 * time.Second
	daemonReapEvery   = time.Minute
)

// Manager 按工作区根管理 daemon 进程。Default 是进程级单例（service
// 接线用它，CloseAll 在服务 Close 收割）。回收循环进程存活期常驻
// （空 map 时每分钟空转一次，代价可忽略）；CloseAll 后 manager 仍可用
// ——下一次查询重新拉起。测试可自建实例避免污染全局。
type Manager struct {
	mu      sync.Mutex
	daemons map[string]*daemon
}

// Default 是服务层接线的进程级 manager。
var Default = NewManager()

// NewManager 建空 manager 并起空闲回收循环。
func NewManager() *Manager {
	m := &Manager{daemons: map[string]*daemon{}}
	go m.reapLoop()
	return m
}

// CloseAll 收割全部 daemon（服务 Close 调用；此后 manager 仍可用——
// 下一次查询会重新拉起）。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.daemons {
		d.close()
	}
	m.daemons = map[string]*daemon{}
}

// reapLoop 每分钟收割空闲超时的 daemon。
func (m *Manager) reapLoop() {
	t := time.NewTicker(daemonReapEvery)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		for root, d := range m.daemons {
			d.mu.Lock()
			idle := time.Since(d.lastUsed) > daemonIdleTimeout
			d.mu.Unlock()
			if idle {
				d.close()
				delete(m.daemons, root)
			}
		}
		m.mu.Unlock()
	}
}

// daemon 是一个工作区根对应的 gopls 进程。
type daemon struct {
	root     string
	mu       sync.Mutex
	cmd      *exec.Cmd
	conn     *rpcConn
	enc      string // "utf-8" | "utf-16"——列单位口径（3.17 协商或缺省）
	ready    bool   // initialize 完成
	broken   bool   // 进程/协议已坏——下次调用换新进程
	lastUsed time.Time
}

// daemonFor 取或建根对应的 daemon（broken 的换新）。
func (m *Manager) daemonFor(root string) (*daemon, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.daemons[root]
	if ok {
		d.mu.Lock()
		br := d.broken
		d.mu.Unlock()
		if !br {
			return d, nil
		}
		d.close() // 换新
	}
	d = &daemon{root: root, lastUsed: time.Now()}
	if err := d.start(); err != nil {
		return nil, err
	}
	m.daemons[root] = d
	return d, nil
}

// goplsPath 是 daemon 拉起的命令（测试注入点；CLI 路径仍走 PATH 查找）。
var goplsPath = "gopls"

// start 拉起 gopls 并完成 initialize（含 utf-8 位置编码协商）。
func (d *daemon) start() error {
	cmd := exec.Command(goplsPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil // gopls 日志默认走 stderr 的 LSP window/logMessage，不混 stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("lsp: start gopls daemon: %w", err)
	}
	conn := newRPCConn(stdin, stdout)
	conn.start()
	d.cmd = cmd
	d.conn = conn

	uri := pathToURI(d.root)
	var result struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	ictx, cancel := context.WithTimeout(context.Background(), daemonInitTimeout)
	defer cancel()
	err = conn.call(ictx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   uri,
		"workspaceFolders": []map[string]any{
			{"uri": uri, "name": filepath.Base(d.root)},
		},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-8"}},
		},
	}, &result)
	if err != nil {
		d.close()
		return fmt.Errorf("lsp: daemon initialize: %w", err)
	}
	if err := conn.notify("initialized", map[string]any{}); err != nil {
		d.close()
		return fmt.Errorf("lsp: daemon initialized notice: %w", err)
	}
	// 位置编码协商：老 gopls（如 v0.16.2）不实现 3.17 协商、缺省即
	// utf-16——按 spec 判定并记录口径，列单位在查询/渲染两侧换算
	//（v1 CLI 对外是字节列，两形态必须一致）。
	switch enc := result.Capabilities.PositionEncoding; enc {
	case "", "utf-16":
		d.enc = "utf-16"
	case "utf-8":
		d.enc = "utf-8"
	default:
		d.close()
		return fmt.Errorf("lsp: daemon position encoding %q unsupported", enc)
	}
	d.ready = true
	d.broken = false
	return nil
}

// close 杀进程关连接（幂等）。
func (d *daemon) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ready = false
	d.broken = true
	if d.conn != nil {
		d.conn.close()
		d.conn = nil
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
		_, _ = d.cmd.Process.Wait()
		d.cmd = nil
	}
}

// call 走 daemon 发请求；broken 重启一次。返回连接级错误时调用方可退
// CLI 路径。
func (m *Manager) call(ctx context.Context, root, method string, params, result any) error {
	_, err := m.callDaemon(ctx, root, method, params, result)
	return err
}

// callDaemon 同 call 但返回 daemon 句柄（查询层拿 enc 做列换算）。
func (m *Manager) callDaemon(ctx context.Context, root, method string, params, result any) (*daemon, error) {
	for attempt := 0; attempt < 2; attempt++ {
		d, err := m.daemonFor(root)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		d.lastUsed = time.Now()
		conn := d.conn
		d.mu.Unlock()
		if conn == nil {
			continue // 竞态下刚被收割——重来一次
		}
		err = conn.call(ctx, method, params, result)
		if err == nil {
			return d, nil
		}
		if err == ErrServerBusy {
			return nil, err
		}
		// 连接级失败：标坏重启重试一次（ctx 截断不重启——那是超时不是崩溃）。
		if ctx.Err() != nil {
			return nil, err
		}
		d.mu.Lock()
		d.broken = true
		d.mu.Unlock()
	}
	return nil, fmt.Errorf("lsp: daemon %s failed twice", method)
}

// NotifyChanged 告知 daemon 文件在磁盘上变了（edit/write 回执后调用，
// 显式失效比依赖 gopls 自带文件监视确定）。
func (m *Manager) NotifyChanged(root, absPath string) {
	d, err := m.daemonFor(root)
	if err != nil {
		return
	}
	d.mu.Lock()
	d.lastUsed = time.Now()
	conn := d.conn
	d.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.notify("workspace/didChangeWatchedFiles", map[string]any{
		"changes": []map[string]any{{"uri": pathToURI(absPath), "type": 2}}, // 2 = Changed
	})
}

// DaemonRun 是 GoplsQuery 兼容入口：definition/references/单文件 check
// 走 daemon；工作区级 check 与未知子命令退 CLI（保 v1 行为）。daemon
// 失败（未装、协商失败、进程两连死）也退 CLI——可用性优先于性能。
func DaemonRun(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	return daemonRunWith(ctx, Default, dir, timeout, args)
}

func daemonRunWith(ctx context.Context, m *Manager, dir string, timeout time.Duration, args []string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return Run(ctx, dir, timeout, args...)
	}
	if text, ok, err := daemonQuery(ctx, m, root, timeout, args); err == nil && ok {
		return text, nil
	} else if err != nil && ctx.Err() == nil {
		// daemon 路径报了真错（如位置参数畸形）——透传给模型自纠。
		if isUsageError(err) {
			return "", err
		}
	}
	// 退化：v1 子进程路径。
	return Run(ctx, dir, timeout, args...)
}

// isUsageError 区分「模型参数错」（透传）与「daemon 基建错」（退 CLI）。
func isUsageError(err error) bool {
	return strings.Contains(err.Error(), "bad position")
}

// daemonQuery 尝试 daemon 路径；ok=false 表示该形态不支持（退 CLI）。
func daemonQuery(ctx context.Context, m *Manager, root string, timeout time.Duration, args []string) (string, bool, error) {
	if len(args) == 0 || runtime.GOOS == "js" {
		return "", false, nil
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch args[0] {
	case "definition", "references":
		file, line, col, err := parsePosArg(args[1:])
		if err != nil {
			return "", true, err
		}
		return m.positionQuery(cctx, root, args[0], file, line, col)
	case "check":
		if len(args) >= 2 && args[1] != "./..." {
			return m.fileDiagnostics(cctx, root, args[1])
		}
		return "", false, nil // 工作区级走 CLI
	default:
		return "", false, nil
	}
}

// positionQuery 发 definition/references，渲染 v1 同形输出（file:line:col
// 1 起，无命中空串）。
func (m *Manager) positionQuery(ctx context.Context, root, mode, file string, line, col int) (string, bool, error) {
	abs := filepath.Join(root, file)
	character := col - 1
	if d, err := m.daemonFor(root); err == nil {
		d.mu.Lock()
		enc := d.enc
		d.mu.Unlock()
		if enc == "utf-16" {
			character = byteToUTF16Col(lineBytes(abs, line), character)
		}
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     map[string]any{"line": line - 1, "character": character},
	}
	if mode == "references" {
		params["context"] = map[string]any{"includeDeclaration": true}
	}
	var raw json.RawMessage
	d, err := m.callDaemon(ctx, root, "textDocument/"+mode, params, &raw)
	if err != nil {
		return "", true, err
	}
	enc := daemonEnc(d)
	// 结果形态：Location | Location[] | null。
	var one struct {
		URI   string   `json:"uri"`
		Range lspRange `json:"range"`
	}
	var many []struct {
		URI   string   `json:"uri"`
		Range lspRange `json:"range"`
	}
	conv := makeConverter(enc)
	var lines []string
	if err := json.Unmarshal(raw, &one); err == nil && one.URI != "" {
		lines = append(lines, renderLocation(root, one.URI, one.Range, conv))
	} else if err := json.Unmarshal(raw, &many); err == nil {
		for _, loc := range many {
			lines = append(lines, renderLocation(root, loc.URI, loc.Range, conv))
		}
	} else {
		var isNull any
		if json.Unmarshal(raw, &isNull) == nil && isNull == nil {
			return "", true, nil // 无命中——v1 同义（CLI 非零退出带空输出）
		}
		return "", true, fmt.Errorf("lsp: %s result shape", mode)
	}
	return strings.Join(lines, "\n"), true, nil
}

// fileDiagnostics 单文件拉取诊断，渲染 file:line:col: message 形态
// （file 相对 root 或绝对路径皆可）。
func (m *Manager) fileDiagnostics(ctx context.Context, root, file string) (string, bool, error) {
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(file))
	}
	var result struct {
		Kind  string `json:"kind"`
		Items []struct {
			Range    lspRange `json:"range"`
			Severity int      `json:"severity"`
			Message  string   `json:"message"`
			Source   string   `json:"source"`
		} `json:"items"`
	}
	err := m.call(ctx, root, "textDocument/diagnostic", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
	}, &result)
	if err != nil {
		return "", true, err
	}
	var b strings.Builder
	for _, it := range result.Items {
		start := it.Range.Start
		fmt.Fprintf(&b, "%s:%d:%d: %s\n", displayPath(root, abs), start.Line+1, start.Character+1, it.Message)
	}
	return strings.TrimRight(b.String(), "\n"), true, nil
}

// lspRange/位置类型（0 基）。
type lspRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
	End struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"end"`
}

// renderLocation 输出 file:line:col-endcol（列 1 起字节口径——与 v1 CLI
// 一致；utf-16 服务端结果按行内容换算回字节列）。
func renderLocation(root, uri string, r lspRange, conv colConverter) string {
	path := uriToPath(uri)
	return fmt.Sprintf("%s:%d:%d-%d", displayPath(root, path), r.Start.Line+1,
		conv(path, r.Start.Line, r.Start.Character)+1, conv(path, r.End.Line, r.End.Character)+1)
}

// displayPath 根内路径用相对形态（与 v1 CLI 输出对齐），根外用原样。
func displayPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// parsePosArg 解析 CLI 同形位置参数 file:line:col（列 1 起）。
func parsePosArg(args []string) (file string, line, col int, err error) {
	if len(args) == 0 {
		return "", 0, 0, fmt.Errorf("lsp: bad position: missing file:line:column")
	}
	parts := strings.Split(args[0], ":")
	if len(parts) < 3 {
		return "", 0, 0, fmt.Errorf("lsp: bad position %q (want file:line:column)", args[0])
	}
	if _, err := fmt.Sscanf(strings.Join(parts[len(parts)-2:], ":"), "%d:%d", &line, &col); err != nil {
		return "", 0, 0, fmt.Errorf("lsp: bad position %q (want file:line:column)", args[0])
	}
	if line <= 0 || col <= 0 {
		return "", 0, 0, fmt.Errorf("lsp: bad position %q (line/column are 1-based)", args[0])
	}
	return strings.Join(parts[:len(parts)-2], ":"), line, col, nil
}

// pathToURI/uriToPath 是 file URI 往返（Windows 盘符形态 file:///D:/…）。
func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	slash := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slash) > 1 && slash[1] == ':' {
		slash = "/" + slash // D:/… → /D:/…——URL 结构体按标准形态拼 file:///
	}
	u := url.URL{Scheme: "file", Path: slash}
	return u.String()
}

func uriToPath(uri string) string {
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		return uri
	}
	// u.Path 已是解码形态（u.RawPath 才是原文）——不能再解一次。
	path := u.Path
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		return filepath.FromSlash(path[1:]) // /D:/… → D:\…
	}
	return filepath.FromSlash(path)
}

// goplsRawStart/debug 仅测试用。
func goplsRawStart() *exec.Cmd { return exec.Command(goplsPath) }

// DaemonProbe 是 edit/write 回灌的 daemon 版探测：先显式失效该文件
// （didChangeWatchedFiles——磁盘改动即刻可见），再走拉取诊断；busy 或
// daemon 不可用时退 v1 CLI Probe（ProbeTimeout 口径不变）。
func DaemonProbe(ctx context.Context, root, absPath string) string {
	Default.NotifyChanged(root, absPath)
	cctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	if text, ok, err := Default.fileDiagnostics(cctx, root, absPath); err == nil && ok {
		return text
	}
	return Probe(ctx, root, absPath)
}

// RunningDaemons 报告常驻 daemon 数（doctor/测试观测面）。
func RunningDaemons() int {
	Default.mu.Lock()
	defer Default.mu.Unlock()
	return len(Default.daemons)
}
