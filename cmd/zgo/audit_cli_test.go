package main

// zgo audit CLI（spec 规则 100）：默认最近条、--days=N 汇总、--on 落
// 设置键、空态提示。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"io"
)

func TestAuditCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dir)
	auditDir := filepath.Join(dir, "audit")
	os.MkdirAll(auditDir, 0o700)
	os.WriteFile(filepath.Join(auditDir, "audit-"+time.Now().Format("20060102")+".jsonl"),
		[]byte(`{"time":"2026-10-20T10:00:00Z","tool":"bash","args":"echo hi","durationMs":5}`+"\n"), 0o600)

	code, out := captureAuditOut(func() int { return runAudit(nil) })
	if code != 0 || !strings.Contains(out, "bash") || !strings.Contains(out, "echo hi") {
		t.Fatalf("recent = %d %q", code, out)
	}
	code, out = captureAuditOut(func() int { return runAudit([]string{"--days=3"}) })
	if code != 0 || !strings.Contains(out, "审计条数") {
		t.Fatalf("days = %d %q", code, out)
	}
	code, out = captureAuditOut(func() int { return runAudit([]string{"--days"}) })
	if code != 1 {
		t.Fatalf("bare --days = %d %q", code, out)
	}
	if code := runAudit([]string{"--on"}); code != 0 {
		t.Fatalf("--on = %d", code)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !strings.Contains(string(raw), "auditLog") {
		t.Fatalf("setting not persisted: %s", raw)
	}
}

func captureAuditOut(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}

// TestAuditQueryFlags（spec 规则 106）：--tool/--decision/--since/--err
// 过滤与坏值拒绝。
func TestAuditQueryFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dir)
	auditDir := filepath.Join(dir, "audit")
	os.MkdirAll(auditDir, 0o700)
	os.WriteFile(filepath.Join(auditDir, "audit-"+time.Now().Format("20060102")+".jsonl"),
		[]byte(`{"time":"2026-10-20T10:00:00Z","tool":"bash","args":"rm x","decision":"ask:denied","isError":true,"durationMs":1}`+"\n"+
			`{"time":"2026-10-20T11:00:00Z","tool":"read","args":"a.go","decision":"capability","durationMs":1}`+"\n"), 0o600)

	code, out := captureAuditOut(func() int { return runAudit([]string{"--decision=ask:denied"}) })
	if code != 0 || !strings.Contains(out, "bash") || strings.Contains(out, "a.go") {
		t.Fatalf("decision filter = %d %q", code, out)
	}
	code, out = captureAuditOut(func() int { return runAudit([]string{"--tool=read"}) })
	if code != 0 || !strings.Contains(out, "a.go") || strings.Contains(out, "rm x") {
		t.Fatalf("tool filter = %d %q", code, out)
	}
	code, out = captureAuditOut(func() int { return runAudit([]string{"--err"}) })
	if code != 0 || !strings.Contains(out, "bash") || strings.Contains(out, "a.go") {
		t.Fatalf("onlyErr = %d %q", code, out)
	}
	if code := runAudit([]string{"--since=7"}); code != 1 {
		t.Fatalf("since without d suffix = %d", code)
	}
	if code := runAudit([]string{"--bogus"}); code != 2 {
		t.Fatalf("unknown flag = %d", code)
	}
}
