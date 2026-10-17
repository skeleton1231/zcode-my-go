package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/schedule"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func jobFor(id, ws string) schedule.Job {
	return schedule.Job{ID: id, Schedule: "*/10 * * * *", Prompt: "检查",
		Workspace: ws, Enabled: true}
}

// TestJobsPanelLifecycle: the automation panel lists jobs, toggles them
// through the service, and the add form validates bad schedules — all via
// the windowless tester.
func TestJobsPanelLifecycle(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := svc.UpsertJob(jobFor("j1", ws)); err != nil {
		t.Fatal(err)
	}

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()

	a.OpenJobs()
	tt.Frame()
	if !tt.HasText("定时任务") || !tt.HasText("j1") || !tt.HasText("*/10 * * * *") {
		t.Fatal("panel does not list the job")
	}

	// 表单校验：坏计划给出错误且不落盘。
	a.jobSchedule = "bad"
	a.jobPrompt = "p"
	a.jobWorkspace = ws
	a.addJobFromForm()
	tt.Frame()
	if a.jobFormErr == "" {
		t.Fatal("bad schedule accepted")
	}
	jobs, _ := svc.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d", len(jobs))
	}

	// 合法计划入库并刷新。
	a.jobSchedule = "*/5 * * * *"
	a.addJobFromForm()
	tt.Frame()
	jobs, _ = svc.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("jobs after add = %d", len(jobs))
	}
	if !tt.HasText("job-") {
		t.Fatal("new job not rendered")
	}

	// 启停与删除都走服务并刷新。
	a.toggleJob("j1", false)
	tt.Frame()
	jobs, _ = svc.Jobs()
	for _, j := range jobs {
		if j.ID == "j1" && j.Enabled {
			t.Fatal("toggle did not persist")
		}
	}
	a.removeJob("j1")
	tt.Frame()
	jobs, _ = svc.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs after remove = %d", len(jobs))
	}
}
