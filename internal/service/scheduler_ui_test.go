package service

import (
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/schedule"
)

func jobFor(id, ws string) schedule.Job {
	return schedule.Job{ID: id, Schedule: "*/10 * * * *", Prompt: "检查",
		Workspace: ws, Enabled: true}
}

// TestJobWrappersRoundTrip: the UI-facing wrappers validate, toggle and
// remove through the store.
func TestJobWrappersRoundTrip(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	// 坏计划被拒。
	bad := jobFor("bad", ws)
	bad.Schedule = "not-a-cron"
	if err := svc.UpsertJob(bad); err == nil {
		t.Fatal("wrapper skipped validation")
	}
	missing := jobFor("empty-ws", "")
	if err := svc.UpsertJob(missing); err == nil {
		t.Fatal("wrapper skipped workspace validation")
	}
	if err := svc.UpsertJob(jobFor("j", ws)); err != nil {
		t.Fatal(err)
	}
	jobs, err := svc.Jobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %v, %v", jobs, err)
	}

	if ok, err := svc.ToggleJob("j", false); err != nil || !ok {
		t.Fatalf("toggle = %v, %v", ok, err)
	}
	jobs, _ = svc.Jobs()
	if jobs[0].Enabled {
		t.Fatal("not disabled")
	}
	if ok, _ := svc.ToggleJob("missing", true); ok {
		t.Fatal("unknown id toggled")
	}
	if ok, err := svc.RemoveJob("j"); err != nil || !ok {
		t.Fatalf("remove = %v, %v", ok, err)
	}
	jobs, _ = svc.Jobs()
	if len(jobs) != 0 {
		t.Fatalf("jobs after remove = %d", len(jobs))
	}
}
