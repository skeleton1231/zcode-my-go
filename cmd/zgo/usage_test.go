package main

import (
	"os"
	"path/filepath"
	"testing"
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
