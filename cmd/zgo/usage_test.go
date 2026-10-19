package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunUsageCLI 验证 zgo usage（spec 规则 25）：无参取最近会话、前缀
// 指定、未知名报错——经 ZGO_TEST_DATADIR 隔离。
func TestRunUsageCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dir)

	// 造一个会话（直接经 service 写盘，runUsage 自开服务读取）。
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 走 print 代价太高：借 sessions 子命令？——直接放一个最小 JSONL。
	id := "20260101T000000.000000001"
	line := `{"type":"meta","id":"` + id + `","title":"用量会话","workspace":"/w"}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, id+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := runUsage(nil); code != 0 {
		t.Fatalf("runUsage() = %d", code)
	}
	if code := runUsage([]string{id}); code != 0 {
		t.Fatalf("runUsage(prefix) = %d", code)
	}
	if code := runUsage([]string{"zzz"}); code == 0 {
		t.Fatal("unknown prefix accepted")
	}
}

// TestUsageHistoryCLI（spec 规则 90）：--days=3 打印趋势（有账日有量、
// 缺日 0）；--days 无值拒绝。
func TestUsageHistoryCLI(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("20060102")
	if err := os.WriteFile(filepath.Join(dir, "usage-"+today+".json"),
		[]byte("{\"tokens\":120}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("ZGO_TEST_DATADIR")
	t.Setenv("ZGO_TEST_DATADIR", dir)
	_ = old

	code, out := captureUsage(func() int { return runUsage([]string{"--days=3"}) })
	if code != 0 || !strings.Contains(out, today) || !strings.Contains(out, "120") ||
		!strings.Contains(out, "▮") {
		t.Fatalf("history = %d %q", code, out)
	}
	if code, _ := captureUsage(func() int { return runUsage([]string{"--days"}) }); code == 0 {
		t.Fatal("bare --days accepted")
	}
}

func captureUsage(f func() int) (int, string) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := f()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return code, string(out)
}
