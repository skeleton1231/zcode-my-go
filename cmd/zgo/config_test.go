package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/service"
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

// TestConfigFallbackModels 兜底链配置面（spec 规则 56）：set 去空去
// 重、清空回空。
func TestConfigFallbackModels(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "fallbackModels", "a,,b,a"}) }); code != 0 {
		t.Fatalf("set code = %d", code)
	}
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if got := svc.FallbackModels(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("models = %v", got)
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "fallbackModels", ""}) }); code != 0 {
		t.Fatalf("clear code = %d", code)
	}
	svc2, _ := service.New(dataDir)
	defer svc2.Close()
	if got := svc2.FallbackModels(); len(got) != 0 {
		t.Fatalf("after clear = %v", got)
	}
}

// TestConfigRedactSecrets（spec 规则 73）：get/set/非法值三态——开关
// 落盘热生效（服务侧 syncRedaction 已有专测，此处只验 CLI 面）。
func TestConfigRedactSecrets(t *testing.T) {
	code, out := captureConfig(t, func() int { return runConfig([]string{"get", "redactSecrets"}) })
	if code != 0 || strings.TrimSpace(out) != "true" {
		t.Fatalf("default should be on: %q (%d)", out, code)
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "redactSecrets", "false"}) }); code != 0 {
		t.Fatal("set false failed")
	}
	code, out = captureConfig(t, func() int { return runConfig([]string{"get", "redactSecrets"}) })
	if code != 0 || strings.TrimSpace(out) != "false" {
		t.Fatalf("after set: %q (%d)", out, code)
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "redactSecrets", "maybe"}) }); code == 0 {
		t.Fatal("non-bool accepted")
	}
	if code, _ := captureConfig(t, func() int { return runConfig([]string{"set", "redactSecrets", "true"}) }); code != 0 {
		t.Fatal("restore true failed")
	}
}
