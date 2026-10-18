package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMCPHangHelper 不是真测试：化身「不说话」的进程——不读 stdin、睡
// 足握手超时的两倍，让 mcp.Start 卡满 20s 握手超时。
func TestMCPHangHelper(t *testing.T) {
	if os.Getenv("ZGO_SVC_MCP_HANG") != "1" {
		return
	}
	time.Sleep(40 * time.Second)
	os.Exit(0)
}

// TestNewDoesNotWaitForMCPHandshake 启动异步（性能路线第二条）：配置一
// 台不说话的服务器（握手卡满 20s 超时），New 必须立即返回——此前同步
// 握手把每个 zgo 命令的启动卡到 20s。
func TestNewDoesNotWaitForMCPHandshake(t *testing.T) {
	dir := t.TempDir()
	settings := `{"mcpServers":{"hang":{"command":"` + filepath.ToSlash(os.Args[0]) +
		`","args":["-test.run=TestMCPHangHelper","--"],"env":{"ZGO_SVC_MCP_HANG":"1"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("New blocked on MCP handshake: %v", elapsed)
	}
	// Close 守卫：在途握手完成后不得入表（进程被关闭而非泄漏）。
	time.Sleep(100 * time.Millisecond)
	svc.mu.Lock()
	n := len(svc.mcp)
	svc.mu.Unlock()
	if n > 1 {
		t.Fatalf("servers leaked: %d", n)
	}
}
