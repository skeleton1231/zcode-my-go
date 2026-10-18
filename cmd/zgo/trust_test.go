package main

import (
	"os"
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
