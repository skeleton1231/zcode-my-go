package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestBugreport 生成自包含报告（spec 规则 60）：结构齐备、脱敏断言
// （API key 值/Hint 不落盘）、指定路径写入。
func TestBugreport(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	// 配一个真 key + 一个会话——报告必须只出现「已配置」，不出现值。
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetAPIKey("sk-secret-abcdef123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.NewSession(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	svc.Close()

	out := filepath.Join(t.TempDir(), "report.md")
	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	code := runBugreport([]string{out})
	os.Stdout = old
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	for _, want := range []string{"# zgo 问题报告", "版本：zgo ", "平台：", "API key：已配置", "会话总数：1", "工具链"} {
		if !strings.Contains(report, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(report, "sk-secret") {
		t.Fatal("API key leaked into report")
	}
	if strings.Contains(report, "Hint") {
		t.Fatal("key hint leaked into report")
	}
}
