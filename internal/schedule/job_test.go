package schedule

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner records runs and can block, fail, or answer.
type fakeRunner struct {
	mu      sync.Mutex
	runs    []string
	block   chan struct{}
	fail    bool
	summary string
}

func (f *fakeRunner) Run(ctx context.Context, job Job) (string, error) {
	f.mu.Lock()
	f.runs = append(f.runs, job.ID)
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	if f.fail {
		return "", errors.New("注入失败")
	}
	if f.summary != "" {
		return f.summary, nil
	}
	return "ran " + job.ID, nil
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func job(id, sched string, enabled bool, lastRun time.Time) Job {
	return Job{ID: id, Schedule: sched, Prompt: "p", Workspace: "/tmp/ws",
		Enabled: enabled, LastRun: lastRun}
}

// waitFor polls until cond passes or the deadline hits（fire 是异步的）.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSchedulerFiresDueAndRecords(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 10, 18, 10, 30, 0, 0, time.Local)
	if err := store.Upsert(job("a", "*/5 * * * *", true, now.Add(-10*time.Minute))); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	s := NewScheduler(store, runner)
	s.now = func() time.Time { return now }

	s.tick(context.Background())

	// 结果落回任务（fire 异步，等它写完）。
	waitFor(t, func() bool {
		jobs, err := store.List()
		return err == nil && len(jobs) == 1 && jobs[0].LastStatus != ""
	})
	jobs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if runner.count() != 1 || jobs[0].LastStatus != "ok" || jobs[0].LastSummary != "ran a" {
		t.Fatalf("runs=%d job=%+v", runner.count(), jobs[0])
	}
	last := jobs[0].LastRun
	if last.IsZero() {
		t.Fatal("lastRun not recorded")
	}

	// 已记录后不再重复触发。
	waitFor(t, func() bool {
		j, err := store.List()
		return err == nil && len(j) == 1 && !j[0].LastRun.IsZero()
	})
	s.tick(context.Background())
	if runner.count() != 1 {
		t.Fatalf("refired: %d", runner.count())
	}
}

func TestSchedulerSkipsDisabledAndOverlaps(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 10, 18, 10, 30, 0, 0, time.Local)
	if err := store.Upsert(job("off", "* * * * *", false, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(job("busy", "* * * * *", true, time.Time{})); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	runner := &fakeRunner{block: release}
	s := NewScheduler(store, runner)
	s.now = func() time.Time { return now }

	s.tick(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for runner.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// 同任务仍在运行：再 tick 应记 skipped 且不启动第二份。
	s.tick(context.Background())
	time.Sleep(100 * time.Millisecond)
	if runner.count() != 1 {
		t.Fatalf("overlapped: %d runs", runner.count())
	}
	jobs, _ := store.List()
	for _, j := range jobs {
		if j.ID == "busy" && j.LastStatus == "" {
			t.Fatal("skipped run not recorded")
		}
		if j.ID == "off" && (j.LastRun != time.Time{} || j.LastStatus != "") {
			t.Fatalf("disabled job ran: %+v", j)
		}
	}
	close(release)
	// 等阻塞的那一轮跑完并落盘，避免 TempDir 清理与写入竞态。
	waitFor(t, func() bool {
		jobs, err := store.List()
		if err != nil {
			return false
		}
		for _, j := range jobs {
			if j.ID == "busy" && j.LastStatus == "ok" {
				return true
			}
		}
		return false
	})
}

func TestSchedulerHotReloads(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 10, 18, 10, 30, 0, 0, time.Local)
	runner := &fakeRunner{}
	s := NewScheduler(store, runner)
	s.now = func() time.Time { return now }

	// 空表 → 无事发生。
	s.tick(context.Background())
	if runner.count() != 0 {
		t.Fatal("fired on empty store")
	}
	// 外部写入后下一 tick 即生效。
	if err := store.Upsert(job("late", "*/30 * * * *", true, time.Time{})); err != nil {
		t.Fatal(err)
	}
	s.tick(context.Background())
	waitFor(t, func() bool { return runner.count() >= 1 })
	if runner.count() != 1 {
		t.Fatalf("hot reload failed: %d", runner.count())
	}
}

func TestSummaryAndErrorRecording(t *testing.T) {
	store := NewStore(t.TempDir())
	runner := &fakeRunner{fail: true}
	s := NewScheduler(store, runner)
	now := time.Date(2026, 10, 18, 10, 30, 0, 0, time.Local)
	s.now = func() time.Time { return now }
	store.Upsert(job("e", "* * * * *", true, time.Time{}))

	if _, err := s.RunNow(context.Background(), job("e", "* * * * *", true, time.Time{})); err == nil {
		t.Fatal("error swallowed")
	}
	jobs, _ := store.List()
	if !strings.Contains(jobs[0].LastStatus, "error") {
		t.Fatalf("status = %q", jobs[0].LastStatus)
	}

	// 摘要截断。
	runner.fail = false
	runner.summary = strings.Repeat("长", SummaryCap+100)
	s.RunNow(context.Background(), job("e", "* * * * *", true, time.Time{}))
	jobs, _ = store.List()
	if len([]rune(jobs[0].LastSummary)) > SummaryCap+1 {
		t.Fatalf("summary not capped: %d runes", len([]rune(jobs[0].LastSummary)))
	}
}
