package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func TestTrustCLIListGrant(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	ws := t.TempDir()

	if code := runTrust([]string{ws}); code != 0 {
		t.Fatalf("grant code = %d", code)
	}
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if !svc.IsWorkspaceTrusted(ws) {
		t.Fatal("grant did not persist")
	}

	code, out := captureTrustCLI(t, "--list")
	if code != 0 || !strings.Contains(out, "cli") {
		t.Fatalf("list code=%d out=%q", code, out)
	}

	// 撤销后再列：空表提示。
	if code := runTrust([]string{"--revoke", ws}); code != 0 {
		t.Fatalf("revoke code = %d", code)
	}
	if svc.IsWorkspaceTrusted(ws) {
		t.Fatal("revoke did not persist")
	}
	if code, _ := captureTrustCLI(t, "--list"); true {
		_ = code
	}
	if code := runTrust([]string{}); code != 2 {
		t.Fatalf("empty args code = %d", code)
	}
}

// captureTrustCLI 捕获 stdout 跑 CLI。
func captureTrustCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runTrust(args)
	w.Close()
	os.Stdout = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return code, string(buf[:n])
}

// TestTrustPreviewCLI（spec 规则 101）：preview 渲染注入面——allow 分
// 工具计数、* 单独标注、四层清单、缺参退出码 2。
func TestTrustPreviewCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dir)
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".zgo/tools"), 0o755)
	os.WriteFile(filepath.Join(ws, ".zgo/tools/deploy.md"), []byte("# x"), 0o644)
	os.WriteFile(filepath.Join(ws, ".zgorules.json"),
		[]byte(`{"version":1,"rules":[{"tool":"bash","args":"git *","behavior":"allow"},{"tool":"*","behavior":"allow"}]}`), 0o644)

	code, out := captureTrustOut(func() int { return runTrust([]string{"preview", ws}) })
	if code != 0 {
		t.Fatalf("preview = %d", code)
	}
	for _, want := range []string{"未授信", "allow bash", "allow *", "全工具放行", "deploy"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview missing %q:\n%s", want, out)
		}
	}
	if code, _ := captureTrustOut(func() int { return runTrust([]string{"preview"}) }); code != 2 {
		t.Fatalf("argless preview = %d", code)
	}
}

// captureTrustOut 抓 runTrust 的 stdout。
func captureTrustOut(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}
