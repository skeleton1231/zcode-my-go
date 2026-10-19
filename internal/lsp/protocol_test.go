package lsp

// JSON-RPC 分帧与连接行为测试：假 LSP 服务器走 test 二进制自重调
//（os.Args[0] -test.run=TestLSPHelperProcess，repo 的 hooks/MCP helper
// 同款）。真 gopls 行为在 daemon_real_test.go（缺 gopls 整组 skip）。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLSPHelperProcess 不是真测试——是被复用的假服务器进程。
func TestLSPHelperProcess(t *testing.T) {
	if os.Getenv("ZGO_LSP_HELPER") == "" {
		return
	}
	defer os.Exit(0)
	br := bufio.NewReaderSize(os.Stdin, 16<<10)
	for {
		msg, err := readFrame(br)
		if err != nil {
			return // stdin 关闭即退
		}
		switch {
		case msg.Method == "initialize":
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"result": map[string]any{"capabilities": map[string]any{"positionEncoding": "utf-8"}},
			})
			// initialize 后立刻发一个服务端主动请求——客户端必须回应
			//（否则真 gopls 握手挂起）。
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage("999"),
				"method": "workspace/configuration",
				"params": map[string]any{"items": []any{map[string]any{"section": "gopls"}}},
			})
		case msg.Method == "workspace/configuration" && string(msg.ID) == "999":
			// 收到客户端对主动请求的回应——用通知把事实转告回去。
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "method": "$/helper/replied", "params": map[string]any{"id": 999},
			})
		case msg.Method == "ping":
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"pong": true},
			})
		case msg.Method == "fail":
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"error": map[string]any{"code": -32603, "message": "boom"},
			})
		case msg.Method == "busy":
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"error": map[string]any{"code": -32800, "message": "not now"},
			})
		case msg.Method == "echo":
			writeFrameOS(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"result": map[string]any{"echo": json.RawMessage(msg.Params)},
			})
		}
	}
}

// writeFrameOS 是 helper 侧的帧写（stdout）。
func writeFrameOS(body any) {
	raw, _ := json.Marshal(body)
	fmt.Printf("Content-Length: %d\r\n\r\n", len(raw))
	os.Stdout.Write(raw)
}

// helperConn 起假服务器进程（test 二进制自重调）并返回其连接。
func helperConn(t *testing.T) (*rpcConn, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestLSPHelperProcess")
	cmd.Env = append(os.Environ(), "ZGO_LSP_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	conn := newRPCConn(stdin, stdout)
	conn.start()
	return conn, func() { conn.close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }
}

func TestRPCRequestResponse(t *testing.T) {
	conn, cleanup := helperConn(t)
	defer cleanup()
	ctx := context.Background()
	// initialize
	var initRes struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	if err := conn.call(ctx, "initialize", map[string]any{}, &initRes); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if initRes.Capabilities.PositionEncoding != "utf-8" {
		t.Fatalf("positionEncoding = %q", initRes.Capabilities.PositionEncoding)
	}
	// 服务端主动请求被回应：等 helper 转告的通知（readLoop 吞通知但 $/
	// helper/replied 到达本身要求 replyServer 已写回应——用 ping 确认
	// 连接仍健康即可：若 replyServer 死锁，ping 会超时）。
	var ping struct {
		Pong bool `json:"pong"`
	}
	if err := conn.call(ctx, "ping", nil, &ping); err != nil {
		t.Fatalf("ping after server-initiated request: %v", err)
	}
	if !ping.Pong {
		t.Fatal("ping result lost")
	}
	// params 往返
	var echo struct {
		Echo map[string]any `json:"echo"`
	}
	if err := conn.call(ctx, "echo", map[string]any{"k": "v"}, &echo); err != nil {
		t.Fatalf("echo: %v", err)
	}
	if echo.Echo["k"] != "v" {
		t.Fatalf("echo params = %v", echo.Echo)
	}
}

func TestRPCErrorShapes(t *testing.T) {
	conn, cleanup := helperConn(t)
	defer cleanup()
	ctx := context.Background()
	err := conn.call(ctx, "fail", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("fail err = %v", err)
	}
	if err := conn.call(ctx, "busy", nil, nil); err != ErrServerBusy {
		t.Fatalf("busy err = %v (want ErrServerBusy)", err)
	}
}

func TestRPCContextCancel(t *testing.T) {
	conn, cleanup := helperConn(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := conn.call(ctx, "never", nil, nil) // helper 不回——等超时
	if err == nil || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("cancel: err=%v elapsed=%v", err, time.Since(start))
	}
	// 取消后连接仍可用于其他请求
	if err := conn.call(context.Background(), "ping", nil, nil); err != nil {
		t.Fatalf("ping after cancel: %v", err)
	}
}

func TestReadFrame(t *testing.T) {
	good := "Content-Length: 2\r\n\r\n{}"
	msg, err := readFrame(bufio.NewReader(strings.NewReader(good)))
	if err != nil || msg == nil {
		t.Fatalf("good frame: %v %v", msg, err)
	}
	if _, err := readFrame(bufio.NewReader(strings.NewReader("X-Bad: 1\r\n\r\n{}"))); err == nil {
		t.Fatal("missing Content-Length accepted")
	}
	if _, err := readFrame(bufio.NewReader(strings.NewReader("Content-Length: abc\r\n\r\n"))); err == nil {
		t.Fatal("bad length accepted")
	}
}

func TestParsePosArg(t *testing.T) {
	f, l, c, err := parsePosArg([]string{"a/b.go:12:34"})
	if err != nil || f != "a/b.go" || l != 12 || c != 34 {
		t.Fatalf("parse = %q %d %d %v", f, l, c, err)
	}
	// Windows 形态盘符路径（D:\x\y.go:1:2 —— 冒号不止两处）
	f, l, c, err = parsePosArg([]string{"D:\\x\\y.go:1:2"})
	if err != nil || f != "D:\\x\\y.go" || l != 1 || c != 2 {
		t.Fatalf("windows parse = %q %d %d %v", f, l, c, err)
	}
	for _, bad := range []string{"a.go", "a.go:x:y", "a.go:0:1", "a.go:1:0"} {
		if _, _, _, err := parsePosArg([]string{bad}); err == nil {
			t.Fatalf("bad %q accepted", bad)
		}
	}
}

func TestURIRoundTrip(t *testing.T) {
	// 用本机临时目录做真路径往返（含空格与 %——两侧转义对称）。
	p := filepath.Join(t.TempDir(), "a b%20.go")
	uri := pathToURI(p)
	back := uriToPath(uri)
	if uri == "" || back != p {
		t.Fatalf("roundtrip %q -> %q -> %q", p, uri, back)
	}
	// Windows 盘符形态只在 Windows 断言（盘符/斜杠语义平台特定——
	// CI ubuntu 上 ToSlash 对反斜杠是 no-op）。
	if runtime.GOOS == "windows" {
		win := pathToURI(`D:\my repo\x.go`)
		if !strings.Contains(win, "/D:/") || !strings.Contains(win, "%20") {
			t.Fatalf("windows uri = %q", win)
		}
	}
}
