package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func runModelsCaptured(t *testing.T, dataDir string, args ...string) (int, string) {
	t.Helper()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runModels(args)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return code, string(out)
}

func TestModelsListAndFilter(t *testing.T) {
	dir := t.TempDir()
	code, out := runModelsCaptured(t, dir)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"模型", "anthropic", "openai", "gemini"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%.400s", want, out)
		}
	}
	// 目录至少有真实条目。
	if n := strings.Count(out, "\n"); n < 5 {
		t.Fatalf("catalog too thin:\n%s", out)
	}

	code, out = runModelsCaptured(t, dir, "--api", "gemini")
	if code != 0 || strings.Contains(out, "anthropic") {
		t.Fatalf("filter leak = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "gemini") {
		t.Fatalf("filter empty:\n%s", out)
	}

	if code, _ := runModelsCaptured(t, dir, "--api", "nope"); code == 0 {
		t.Fatal("unknown api accepted")
	}
}

func TestModelsMarksConfiguredAndWarns(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"api":"anthropic","apiKey":"sk-x","model":"my-finetune-x99"}`)
	code, out := runModelsCaptured(t, dir)
	if code != 0 || !strings.Contains(out, "my-finetune-x99") {
		t.Fatalf("unknown configured model missing from warning: %d\n%s", code, out)
	}
	if !strings.Contains(out, "不在目录") {
		t.Fatalf("unknown-model warning missing:\n%s", out)
	}
	// 目录内模型不告警、并带 ← 当前 标记。
	writeSettings(t, dir, `{"api":"anthropic","apiKey":"sk-x","model":"glm-5.2"}`)
	_, out = runModelsCaptured(t, dir)
	if strings.Contains(out, "不在目录") {
		t.Fatal("known model warned")
	}
	if !strings.Contains(out, "← 当前") {
		t.Fatalf("configured mark missing:\n%s", out)
	}
}

func TestModelsCheck(t *testing.T) {
	dir := t.TempDir()
	code, out := runModelsCaptured(t, dir, "--check", "claude-sonnet-4-5")
	if code != 0 || !strings.Contains(out, "✓") {
		t.Fatalf("check known = %d %q", code, out)
	}
	// 家族变体（日期后缀）。
	if ai.KnownModel("claude-sonnet-4-5-20261001") {
		code, out = runModelsCaptured(t, dir, "--check", "claude-sonnet-4-5-20261001")
		if code != 0 || !strings.Contains(out, "✓") {
			t.Fatalf("check variant = %d %q", code, out)
		}
	}
	code, out = runModelsCaptured(t, dir, "--check", "totally-unknown-model")
	if code != 0 || !strings.Contains(out, "不在目录") {
		t.Fatalf("check unknown = %d %q", code, out)
	}
	if code, _ := runModelsCaptured(t, dir, "--check", ""); code == 0 {
		t.Fatal("empty check accepted")
	}
}

// writeSettings plants a settings file（doctor 测试同款）.
func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/settings.json", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
