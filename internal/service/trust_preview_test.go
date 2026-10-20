package service

// 信任前预览测试（spec 规则 101）：五层注入面汇总、allow 按工具计数
//（* 单列）、坏规则文件诚实标注、干净工作区 Empty、非目录拒绝。

import (
	"os"
	"path/filepath"
	"testing"
)

func seedTrustLayer(t *testing.T, ws string) {
	t.Helper()
	mk := func(rel string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(ws, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, rel), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(".zgo/tools/deploy.md")
	mk(".zgo/commands/review.md")
	mk(".zgo/skills/lint/SKILL.md")
	// 子代理定义要 frontmatter（name/description 是路由依据）。
	if err := os.MkdirAll(filepath.Join(ws, ".zgo/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ws, ".zgo/agents/auditor.md"),
		[]byte("---\nname: auditor\ndescription: 审计复核\n---\n你是审计员。\n"), 0o644)
}

func TestTrustPreviewReport(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ws := t.TempDir()
	seedTrustLayer(t, ws)
	os.WriteFile(filepath.Join(ws, ".zgorules.json"), []byte(`{"version":1,"rules":[
		{"tool":"bash","args":"git *","behavior":"allow"},
		{"tool":"bash","args":"rm *","behavior":"deny"},
		{"tool":"read","behavior":"allow"},
		{"tool":"*","behavior":"allow"}
	]}`), 0o644)

	rep, err := svc.TrustPreview(ws)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Trusted {
		t.Fatal("untrusted workspace reported trusted")
	}
	if rep.AllowRules["bash"] != 1 || rep.AllowRules["read"] != 1 || rep.AllowRules["*"] != 1 {
		t.Fatalf("allow rules = %v", rep.AllowRules)
	}
	if rep.DenyRules != 1 || rep.AskRules != 0 {
		t.Fatalf("deny/ask = %d/%d", rep.DenyRules, rep.AskRules)
	}
	if len(rep.Tools) != 1 || rep.Tools[0] != "deploy" {
		t.Fatalf("tools = %v", rep.Tools)
	}
	if len(rep.Commands) != 1 || len(rep.Skills) != 1 || len(rep.Agents) != 1 {
		t.Fatalf("layers = %v %v %v", rep.Commands, rep.Skills, rep.Agents)
	}
	if rep.Empty() {
		t.Fatal("layered workspace reported empty")
	}

	// 授信后状态翻转。
	if err := svc.TrustWorkspace(ws, "test"); err != nil {
		t.Fatal(err)
	}
	if rep, _ = svc.TrustPreview(ws); !rep.Trusted {
		t.Fatal("trust state not reflected")
	}
}

func TestTrustPreviewEdgeCases(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	// 干净目录。
	clean := t.TempDir()
	rep, err := svc.TrustPreview(clean)
	if err != nil || !rep.Empty() {
		t.Fatalf("clean = %+v %v", rep, err)
	}
	// 坏规则文件。
	broken := t.TempDir()
	os.WriteFile(filepath.Join(broken, ".zgorules.json"), []byte(`{oops`), 0o644)
	rep, err = svc.TrustPreview(broken)
	if err != nil || !rep.RulesBroken || len(rep.AllowRules) != 0 {
		t.Fatalf("broken = %+v %v", rep, err)
	}
	// 非目录。
	if _, err := svc.TrustPreview(filepath.Join(clean, "nope")); err == nil {
		t.Fatal("missing dir accepted")
	}
}

// TestAgentsLayerTrustGate（spec 规则 116）：agents 层此前未过信任门
//（persona 是提示注入面）——未授信工作区不列其层；授信后出现。
func TestAgentsLayerTrustGate(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".zgo", "agents"), 0o755)
	os.WriteFile(filepath.Join(ws, ".zgo", "agents", "spy.md"),
		[]byte("---\nname: spy\ndescription: 潜伏\n---\n你是间谍。\n"), 0o644)

	out := svc.Agents(ws)
	for _, d := range out.Defs {
		if d.Name == "spy" {
			t.Fatal("untrusted agent layer visible")
		}
	}
	if err := svc.TrustWorkspace(ws, "test"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range svc.Agents(ws).Defs {
		if d.Name == "spy" {
			found = true
		}
	}
	if !found {
		t.Fatal("trusted agent layer missing")
	}
}
