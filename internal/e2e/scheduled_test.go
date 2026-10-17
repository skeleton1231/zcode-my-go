package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/schedule"
)

// TestE2ERunJobNowDrivesAgent: `zgo cron run` 的服务路径——RunJobNow 走
// scheduleRunner：新建会话、一次完整 Chat（脚本 provider）、末条 assistant
// 文本作为摘要、状态与时间记录回任务、会话转录落盘可查。
func TestE2ERunJobNowDrivesAgent(t *testing.T) {
	h := New(t, Turn{Text: "定时任务回答：一切正常"})

	store := h.Svc.JobStore()
	if err := store.Upsert(schedule.Job{
		ID: "e2e", Schedule: "0 0 31 2 *", // 计划永不到期也无妨：RunNow 直接执行
		Prompt: "检查一下", Workspace: h.Workspace, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	summary, err := h.Svc.RunJobNow(context.Background(), "e2e")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if summary == "" || summary != "定时任务回答：一切正常" {
		t.Fatalf("summary = %q", summary)
	}

	jobs, err := store.List()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %v, %v", jobs, err)
	}
	if jobs[0].LastStatus != "ok" || jobs[0].LastRun.IsZero() {
		t.Fatalf("job = %+v", jobs[0])
	}

	// 运行产生了真实会话（harness 自带一个，任务新建一个）。
	sessions := h.Svc.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (harness + job run)", len(sessions))
	}
	var jobSession string
	for _, s := range sessions {
		if s.ID != h.SessionID {
			jobSession = s.ID
		}
	}
	if jobSession == "" {
		t.Fatal("job session not found")
	}
	jobMsgs, err := h.Svc.Messages(jobSession)
	if err != nil || len(jobMsgs) < 2 {
		t.Fatalf("job session transcript = %d, %v", len(jobMsgs), err)
	}
}

// TestE2ERunJobNowMissingJob: unknown IDs are an explicit error.
func TestE2ERunJobNowMissingJob(t *testing.T) {
	h := New(t, Turn{Text: "x"})
	if _, err := h.Svc.RunJobNow(context.Background(), "nope"); err == nil {
		t.Fatal("missing job swallowed")
	}
}

// TestE2ESchedulerTickFiresThroughService: a due job fires through the
// REAL minute-tick scheduler wired on the service (clock injected, one
// manual tick) — the run goes through the scheduleRunner into a live
// session; disabled jobs stay untouched; Stop joins cleanly.
func TestE2ESchedulerTickFiresThroughService(t *testing.T) {
	h := New(t, Turn{Text: "定时回答：完成"})
	store := h.Svc.JobStore()
	if err := store.Upsert(schedule.Job{
		ID: "tick", Schedule: "* * * * *", Prompt: "跑一遍",
		Workspace: h.Workspace, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(schedule.Job{
		ID: "off", Schedule: "* * * * *", Prompt: "不该跑",
		Workspace: h.Workspace, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	h.Svc.StartScheduler()
	defer h.Svc.StopScheduler()
	sched := h.Svc.Scheduler()
	if sched == nil {
		t.Fatal("scheduler not running")
	}
	fixed := time.Now().Truncate(time.Minute)
	sched.SetClock(func() time.Time { return fixed })

	sched.FireDue(context.Background())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := store.List()
		if err == nil && len(jobs) == 2 {
			tickOK, offIdle := false, false
			for _, j := range jobs {
				if j.ID == "tick" && j.LastStatus == "ok" && !j.LastRun.IsZero() {
					tickOK = true
				}
				if j.ID == "off" && j.LastRun.IsZero() {
					offIdle = true
				}
			}
			if tickOK && offIdle {
				// 任务产生了独立会话转录。
				sessions := h.Svc.ListSessions()
				if len(sessions) != 2 { // harness + job run
					t.Fatalf("sessions = %d", len(sessions))
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	jobs, _ := store.List()
	t.Fatalf("tick never fired cleanly: %+v", jobs)
}
