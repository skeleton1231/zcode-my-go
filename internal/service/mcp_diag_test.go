package service

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/mcp"
)

// TestMCPDiagnosticsEmptyAndBroken: no servers → no reports; a broken
// one reports red without blocking the empty case.
func TestMCPDiagnosticsEmptyAndBroken(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)

	if got := svc.MCPDiagnostics(); len(got) != 0 {
		t.Fatalf("empty reports = %+v", got)
	}

	// 直接注入一台坏服务器（settings 字段同包可达；不触发 syncMCP 的
	// 启动路径——诊断自己拉新实例）。
	svc.mu.Lock()
	svc.settings.MCPServers = map[string]mcp.Config{"broken": {Command: "definitely-not-a-command-xyz"}}
	svc.mu.Unlock()

	reports := svc.MCPDiagnostics()
	if len(reports) != 1 || reports[0].OK || !strings.Contains(reports[0].Error, "not-a-command") {
		t.Fatalf("reports = %+v", reports)
	}
	if !strings.Contains(reports[0].Line(), "broken：✕") {
		t.Fatalf("line = %q", reports[0].Line())
	}
}
