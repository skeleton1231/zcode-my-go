package service

// RunDueOnce 测试（spec 规则 123，zgo cron serve --once 的执行面）：到
// 期任务真实跑完（脚本 provider 回放）、运行历史与 LastRun 落定后才返
// 回；未到期/禁用不跑。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/schedule"
)

func TestRunDueOnceFiresDueJob(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	// 脚本回放：一轮文本即收尾。
	out := make(chan ai.StreamEvent, 4)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "夜间任务完成：索引重建 42 个"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	svc.UseProviderForTesting(func() ai.Provider { return chProvider{ch: out} })

	ws := t.TempDir()
	if err := svc.UpsertJob(schedule.Job{
		ID: "due", Schedule: "* * * * *", Prompt: "重建索引", Workspace: ws, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 未到期（LastRun 已是未来一分钟）与禁用各一个——都不该跑。
	future := time.Now().Add(time.Minute)
	if err := svc.UpsertJob(schedule.Job{
		ID: "notyet", Schedule: "0 0 1 1 *", Prompt: "x", Workspace: ws, LastRun: future, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertJob(schedule.Job{
		ID: "off", Schedule: "* * * * *", Prompt: "x", Workspace: ws, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ToggleJob("off", false); err != nil {
		t.Fatal(err)
	}

	svc.RunDueOnce(context.Background())

	hist := schedule.RunHistory(dataDir, "due", 10)
	if len(hist) != 1 || hist[0].Status != "ok" {
		t.Fatalf("due history = %+v", hist)
	}
	if hist[0].Source != "tick" {
		t.Fatalf("source = %q", hist[0].Source)
	}
	if got := schedule.RunHistory(dataDir, "notyet", 10); len(got) != 0 {
		t.Fatalf("notyet fired: %+v", got)
	}
	if got := schedule.RunHistory(dataDir, "off", 10); len(got) != 0 {
		t.Fatalf("disabled fired: %+v", got)
	}
	// LastRun 落定（下轮不再补跑）。
	jobs, err := svc.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID == "due" && j.LastRun.IsZero() {
			t.Fatal("due LastRun not updated")
		}
	}
}

// TestRunDueOnceNoJobs：无任务时静默返回（定时器拉起是常态路径）。
func TestRunDueOnceNoJobs(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.RunDueOnce(context.Background())
	if _, err := os.Stat(filepath.Join(dataDir, "job-logs")); !os.IsNotExist(err) {
		t.Fatalf("job-logs should not exist: %v", err)
	}
}
