package main

import "testing"

// TestMCPServeArgGuard（spec 规则 92）：未知参数拒绝（退出码 2，不进
// stdin 循环）；--all 合法路径的细节由 internal/mcpserver 包测覆盖。
func TestMCPServeArgGuard(t *testing.T) {
	if code := runMCPServe([]string{"--http"}); code != 2 {
		t.Fatalf("unknown flag accepted: code=%d", code)
	}
}
