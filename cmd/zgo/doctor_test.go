package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureDoctor runs runDoctor with stdout captured and ZGO_TEST_DATADIR
// pointing at a fresh dir.
func captureDoctor(t *testing.T, dataDir string) (int, string) {
	t.Helper()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runDoctor()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return code, string(out)
}

func TestDoctorFreshInstall(t *testing.T) {
	code, out := captureDoctor(t, t.TempDir())
	if code != 0 {
		t.Fatalf("code = %d, out = %s", code, out)
	}
	// 未配置 key / 未配 MCP / 工具链检查都在场。
	for _, want := range []string{"API key 未配置", "未配置 MCP 服务器", "git 可用"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// key 绝不回显：输出里不应出现任何疑似密钥的字段。
	if strings.Contains(out, "\"apiKey\"") {
		t.Fatal("settings leaked into doctor output")
	}
}

func TestDoctorConfiguredAndMCP(t *testing.T) {
	dataDir := t.TempDir()
	// 预置一份带 key/model 的设置与一台坏 MCP 服务器。
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"api":"anthropic","apiKey":"sk-test-123","model":"claude-sonnet-4-5",
		"mcpServers":{"broken":{"command":"definitely-not-a-command-xyz"}}}`
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := captureDoctor(t, dataDir)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "provider 已配置") {
		t.Fatalf("provider line missing:\n%s", out)
	}
	if !strings.Contains(out, "broken：✕") {
		t.Fatalf("broken MCP not reported:\n%s", out)
	}
	if strings.Contains(out, "sk-test-123") {
		t.Fatal("API key echoed back")
	}
}
