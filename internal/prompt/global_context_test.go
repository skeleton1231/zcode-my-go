package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGlobalContextDiscoversAgentsMd: 用户全局层按同一 contextFiles 序
// 发现（issue #217：只认 ZGO.md——用户的「所有项目通用偏好」以
// AGENTS.md 心智写的无处安放）；ZGO.md 优先，无 ZGO 时 AGENTS 兜底。
func TestGlobalContextDiscoversAgentsMd(t *testing.T) {
	home := hermeticContext(t)

	// AGENTS.md 单独在场：作为用户全局注入，且先于项目层。
	os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("用户全局：永远先跑测试"), 0o644)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("项目级规则"), 0o644)
	got := System(ws)
	if !strings.Contains(got, "用户全局：永远先跑测试") {
		t.Fatalf("user AGENTS.md not injected:\n%s", got)
	}
	if !strings.Contains(got, "项目级规则") {
		t.Fatalf("project layer lost:\n%s", got)
	}
	if strings.Index(got, "用户全局") > strings.Index(got, "项目级规则") {
		t.Fatal("user global must precede project layer")
	}

	// ZGO.md 优先于 AGENTS.md（同 contextFiles 序）。
	os.WriteFile(filepath.Join(home, "ZGO.md"), []byte("ZGO 全局"), 0o644)
	got2 := System(ws)
	if !strings.Contains(got2, "ZGO 全局") || strings.Contains(got2, "用户全局：永远先跑测试") {
		t.Fatalf("ZGO.md should win over AGENTS.md:\n%s", got2)
	}
}

// TestGlobalContextCapped: 用户全局层超 8KB 截断（对齐注入家族上限）。
func TestGlobalContextCapped(t *testing.T) {
	home := hermeticContext(t)
	big := strings.Repeat("字", 6000) // ~18KB UTF-8
	os.WriteFile(filepath.Join(home, "CLAUDE.md"), []byte(big), 0o644)
	got := System(t.TempDir())
	if len(got) > contextCapBytes+8192 {
		t.Fatalf("global context uncapped: %d bytes", len(got))
	}
}
