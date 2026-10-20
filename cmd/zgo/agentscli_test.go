package main

// zgo agents CLI 测试（spec 规则 116）：信任门两态（未授信不列工作区
// 层）、list 形态、show 原文、退出码矩阵。

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentsCapture(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}

func TestAgentsCLI(t *testing.T) {
	t.Setenv("ZGO_TEST_DATADIR", t.TempDir())
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".zgo", "agents"), 0o755)
	os.WriteFile(filepath.Join(ws, ".zgo", "agents", "auditor.md"),
		[]byte("---\nname: auditor\ndescription: 审计复核\ntools: read,grep\n---\n你是审计员。\n"), 0o644)

	// 未授信：工作区层不出现，空态提示。
	code, out := agentsCapture(func() int { return runAgents([]string{"list", ws}) })
	if code != 0 || strings.Contains(out, "auditor") {
		t.Fatalf("untrusted layer leaked: %d\n%s", code, out)
	}
	if !strings.Contains(out, "未发现子代理定义") {
		t.Fatalf("empty state missing:\n%s", out)
	}
	// 授信后：出现，带白名单与描述。
	if c := runTrust([]string{ws}); c != 0 {
		t.Fatalf("trust = %d", c)
	}
	code, out = agentsCapture(func() int { return runAgents([]string{"list", ws}) })
	if code != 0 || !strings.Contains(out, "auditor") || !strings.Contains(out, "审计复核") ||
		!strings.Contains(out, "read,grep") || !strings.Contains(out, "工作区") {
		t.Fatalf("list missing def:\n%s", out)
	}
	// show：frontmatter + persona 原文。
	code, out = agentsCapture(func() int { return runAgents([]string{"show", "auditor", ws}) })
	if code != 0 || !strings.Contains(out, "你是审计员") || !strings.Contains(out, "name: auditor") {
		t.Fatalf("show = %d\n%s", code, out)
	}
	if code := runAgents([]string{"show", "nope"}); code != 1 {
		t.Fatalf("unknown = %d", code)
	}
	if code := runAgents([]string{"show"}); code != 2 {
		t.Fatalf("argless = %d", code)
	}
	if code := runAgents([]string{"bogus"}); code != 2 {
		t.Fatalf("bogus = %d", code)
	}
}
