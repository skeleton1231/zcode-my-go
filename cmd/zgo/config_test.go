package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureConfig 把 config 子命令的 stdout 捕获成串（os.Pipe 换向）。
func captureConfig(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}

// TestConfigListGetSet 验证 config 三形态（spec 规则 37）：list 全量
// 且 apiKey 只显 Hint、get 单值、set 各键校验与热生效。
func TestConfigListGetSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dir)

	code, out := captureConfig(t, func() int { return runConfig([]string{"list"}) })
	if code != 0 || !strings.Contains(out, "model") || !strings.Contains(out, "budgetDailyTokens 0") {
		t.Fatalf("list = %d %q", code, out)
	}

	// set 各键。
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "model", "glm-5.2"}) }); code != 0 {
		t.Fatal("set model failed")
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "budgetDailyTokens", "50000"}) }); code != 0 {
		t.Fatal("set budget failed")
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "apiKey", "sk-secret-1234567890abcd"}) }); code != 0 {
		t.Fatal("set apiKey failed")
	}
	code, out = captureConfig(t, func() int { return runConfig([]string{"get", "apiKey"}) })
	if code != 0 || strings.Contains(out, "sk-secret") {
		t.Fatalf("key leaked: %q", out)
	}
	code, out = captureConfig(t, func() int { return runConfig([]string{"get", "budgetDailyTokens"}) })
	if code != 0 || strings.TrimSpace(out) != "50000" {
		t.Fatalf("budget get = %q", out)
	}
	code, out = captureConfig(t, func() int { return runConfig([]string{"get", "model"}) })
	if code != 0 || strings.TrimSpace(out) != "glm-5.2" {
		t.Fatalf("model get = %q", out)
	}

	// 非法值拒绝。
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "api", "xml"}) }); code == 0 {
		t.Fatal("invalid api accepted")
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "budgetDailyTokens", "-1"}) }); code == 0 {
		t.Fatal("negative budget accepted")
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "nope", "x"}) }); code == 0 {
		t.Fatal("unknown key accepted")
	}
}
