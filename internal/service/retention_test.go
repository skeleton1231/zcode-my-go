package service

// 保留期归档测试（spec 规则 98）：过期移入 archive/、近期保留、关闭
// 不动、恢复回活跃面且刷新 mtime、组织态 archive 与保留期正交。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedSessionFile(t *testing.T, dir, name string, ageDays int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","blocks":[{"kind":"text","text":"hi"}]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -ageDays)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSweepStaleSessions(t *testing.T) {
	dir := t.TempDir()
	arch := filepath.Join(t.TempDir(), "archive")
	seedSessionFile(t, dir, "old.jsonl", 40)
	seedSessionFile(t, dir, "recent.jsonl", 3)
	seedSessionFile(t, dir, "edge.jsonl", 9) // 边界内侧（9 天 < 10 天保留）——精确边界受 FS 时间粒度影响不测
	seedSessionFile(t, dir, "note.txt", 40)  // 非 jsonl 不动

	if n := sweepStaleSessions(dir, arch, 10); n != 1 {
		t.Fatalf("moved = %d", n)
	}
	if _, err := os.Stat(filepath.Join(arch, "old.jsonl")); err != nil {
		t.Fatal("old not archived")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.jsonl")); !os.IsNotExist(err) {
		t.Fatal("old still in sessions")
	}
	for _, keep := range []string{"recent.jsonl", "edge.jsonl", "note.txt"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("%s lost: %v", keep, err)
		}
	}
	// 关闭（0）不动任何东西。
	seedSessionFile(t, dir, "ancient.jsonl", 365)
	if n := sweepStaleSessions(dir, arch, 0); n != 0 {
		t.Fatalf("disabled moved = %d", n)
	}
	// 收紧到 5 天：edge（9 天）与 ancient（365 天）入档；old 已在
	// 归档目录（同名冲突）跳过——不覆盖既有归档。
	if n := sweepStaleSessions(dir, arch, 5); n != 2 {
		t.Fatalf("tightened move = %d", n)
	}
	if _, err := os.Stat(filepath.Join(arch, "old.jsonl")); err != nil {
		t.Fatal("archive copy lost")
	}
}

func TestRetentionWiredAtStartup(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]int{"sessionRetentionDays": 30})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// 用真实服务建两个会话（合法 JSONL），关掉后把一个的 mtime 拨老。
	boot, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	liveInfo, err := boot.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	staleInfo, err := boot.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	boot.Close()
	old := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(filepath.Join(dir, "sessions", staleInfo.ID+".jsonl"), old, old); err != nil {
		t.Fatal(err)
	}

	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, err := os.Stat(filepath.Join(dir, "archive", staleInfo.ID+".jsonl")); err != nil {
		t.Fatal("startup sweep missing")
	}
	// 活跃面收敛：list 只剩 live。
	ids := svc.ListSessions()
	if len(ids) != 1 || ids[0].ID != liveInfo.ID {
		t.Fatalf("active sessions = %+v", ids)
	}
	// retired/restore 面。
	if got := svc.ArchivedSessions(); len(got) != 1 || got[0] != staleInfo.ID+".jsonl" {
		t.Fatalf("retired = %v", got)
	}
	if err := svc.RestoreArchivedSession(staleInfo.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", staleInfo.ID+".jsonl")); err != nil {
		t.Fatal("restore did not move back")
	}
	// 恢复后 mtime 已刷新——再次 sweep 不会被立刻再归档。
	sweepStaleSessions(filepath.Join(dir, "sessions"), filepath.Join(dir, "archive"), 30)
	if _, err := os.Stat(filepath.Join(dir, "sessions", staleInfo.ID+".jsonl")); err != nil {
		t.Fatal("restored session re-archived immediately")
	}
	// 活跃同名不覆盖。
	os.MkdirAll(filepath.Join(dir, "archive"), 0o700)
	os.WriteFile(filepath.Join(dir, "archive", liveInfo.ID+".jsonl"), []byte("x"), 0o600)
	if err := svc.RestoreArchivedSession(liveInfo.ID + ".jsonl"); err == nil {
		t.Fatal("restore over active accepted")
	}
}
