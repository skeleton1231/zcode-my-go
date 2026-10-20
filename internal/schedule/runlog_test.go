package schedule

// 运行历史测试（spec 规则 118）：recordWithTime 落史、RunHistory 新到
// 旧、环形裁剪、limit。

import (
	"context"
	"testing"
	"time"
)

func TestRunHistoryAppendedAndRead(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	job := Job{ID: "hist", Schedule: "0 3 * * *", Prompt: "p", Workspace: t.TempDir(), Enabled: true}
	store.Upsert(job)
	runs := 0
	sch := NewScheduler(store, stubRunner(func(ctx context.Context, j Job) (string, error) {
		runs++
		return "第 " + time.Now().Format("04") + " 次结论", nil
	}))
	base := time.Date(2026, 10, 19, 3, 0, 0, 0, time.Local)
	sch.recordWithTime("hist", "ok", "结论一", base)
	sch.recordWithTime("hist", "ok", "结论二", base.Add(time.Hour))
	sch.recordWithTime("hist", "error: 网络抖动", "", base.Add(2*time.Hour))

	entries := RunHistory(dir, "hist", 10)
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Status != "error: 网络抖动" || entries[2].Summary != "结论一" {
		t.Fatalf("order wrong: %+v", entries)
	}
	if got := RunHistory(dir, "hist", 2); len(got) != 2 || got[0].Status != "error: 网络抖动" {
		t.Fatalf("limit = %+v", got)
	}
	if got := RunHistory(dir, "nojob", 10); got != nil {
		t.Fatalf("unknown job = %v", got)
	}
}

func TestRunHistoryRingTrim(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	store.Upsert(Job{ID: "ring", Schedule: "0 3 * * *", Prompt: "p", Workspace: t.TempDir(), Enabled: true})
	sch := NewScheduler(store, stubRunner(func(ctx context.Context, j Job) (string, error) { return "", nil }))
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i < runLogMax+30; i++ {
		sch.recordWithTime("ring", "ok", "r", base.Add(time.Duration(i)*time.Minute))
	}
	entries := RunHistory(dir, "ring", 1000)
	if len(entries) != runLogMax {
		t.Fatalf("ring size = %d want %d", len(entries), runLogMax)
	}
	// 最旧被裁：最早保留的是第 30 条（0 起）。
	if !entries[len(entries)-1].Time.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("oldest kept = %v", entries[len(entries)-1].Time)
	}
}

// stubRunner 测试桩（Runner 接口无 func 形态）。
type stubRunner func(ctx context.Context, job Job) (string, error)

func (f stubRunner) Run(ctx context.Context, job Job) (string, error) { return f(ctx, job) }
