package service

import (
	"context"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
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

// TestScheduleRunnerUsesJobModel 任务级模型（spec 规则 60）：scheduleRunner
// 对 job.Model 非空的任务把会话模型覆盖为任务模型——脚本 provider 捕获
// 请求的 Model 字段断言；空模型不动全局。
func TestScheduleRunnerUsesJobModel(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	var gotModel string
	prov := captureModelProvider{model: &gotModel}
	svc.UseProviderForTesting(func() ai.Provider { return prov })
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })

	r := scheduleRunner{svc}
	if _, err := r.Run(context.Background(), schedule.Job{
		Workspace: t.TempDir(), Prompt: "跑", Model: "glm-cheap"}); err != nil {
		t.Fatal(err)
	}
	if gotModel != "glm-cheap" {
		t.Fatalf("request model = %q", gotModel)
	}
	// 空模型：沿用全局（settings 默认，非空即可区分）。
	gotModel = ""
	if _, err := r.Run(context.Background(), schedule.Job{
		Workspace: t.TempDir(), Prompt: "跑"}); err != nil {
		t.Fatal(err)
	}
	if gotModel == "glm-cheap" {
		t.Fatal("empty job model overrode global")
	}
}

// captureModelProvider 记录每次请求的 Model 并回一个文本回合。
type captureModelProvider struct{ model *string }

func (p captureModelProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	*p.model = req.Model
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "完成"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}
