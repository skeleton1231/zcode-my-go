package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func steps(n ...string) []agent.WorkflowStep {
	var out []agent.WorkflowStep
	for _, d := range n {
		out = append(out, agent.WorkflowStep{Description: d, Kind: "research"})
	}
	return out
}

func TestStoreDefinitionRoundtrip(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(Definition{Name: "release-check", Steps: steps("扫描", "复核")}); err != nil {
		t.Fatal(err)
	}
	def, err := s.Get("release-check")
	if err != nil || len(def.Steps) != 2 || def.SavedAt.IsZero() {
		t.Fatalf("get: %v %+v", err, def)
	}
	list := s.List()
	if len(list) != 1 || list[0].Name != "release-check" {
		t.Fatalf("list: %+v", list)
	}
	if _, err := s.Get("nope"); err == nil {
		t.Fatal("missing def accepted")
	}
}

func TestStoreNameValidation(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, bad := range []string{"", "UPPER", "has space", "slash/name", longName + "x", "中文名"} {
		if err := s.Save(Definition{Name: bad, Steps: steps("a")}); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
	if err := s.Save(Definition{Name: "ok-name-2", Steps: nil}); err == nil {
		t.Error("empty steps accepted")
	}
}

const longName = "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz0123456789abcdef"

func TestRunLifecycle(t *testing.T) {
	s := NewStore(t.TempDir())
	run, err := s.StartRun("release-check", "D:/ws", steps("扫描", "复核", "发布"))
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" || len(run.Steps) != 3 || run.Steps[1].Index != 1 {
		t.Fatalf("start: %+v", run)
	}
	// 中途读：running 已落盘（崩溃可续跑锚点）。
	got, err := s.GetRun(run.ID)
	if err != nil || got.Status != "running" {
		t.Fatalf("getrun: %v %+v", err, got)
	}
	// 前两步成功、第三步未跑（顺序模式失败即止返回前缀）。
	results := []agent.StepResult{
		{Step: steps("扫描")[0], Text: "发现 3 处"},
		{Step: steps("复核")[0], Text: "通过"},
	}
	if err := s.FinishRun(run, results, "failed", "step 3 (edit) failed: boom"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.Error == "" {
		t.Fatalf("finish: %+v", got)
	}
	if !got.Steps[0].Done || !got.Steps[1].Done || got.Steps[2].Done || got.Steps[2].Text != "" {
		t.Fatalf("step states: %+v", got.Steps)
	}
	runs := s.ListRuns(10)
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("listruns: %+v", runs)
	}
}

func TestPrefillOf(t *testing.T) {
	run := Run{Steps: []RunStep{
		{Index: 0, Kind: "research", Description: "扫描", Text: "发现 3 处", Done: true},
		{Index: 1, Kind: "research", Description: "复核", Text: "失败记录", Failed: true},
		{Index: 2, Kind: "edit", Description: "发布"},
	}}
	prefill := run.PrefillOf(steps("扫描", "复核", "发布"))
	if len(prefill) != 1 {
		t.Fatalf("prefill = %+v（只应有完成的第 0 步）", prefill)
	}
	if prefill[0].Text != "发现 3 处" {
		t.Fatalf("prefill text: %+v", prefill[0])
	}
	// 定义漂移：描述变化后不注入（以定义为准重跑）。
	drifted := []agent.WorkflowStep{
		{Description: "重新扫描", Kind: "research"},
		{Description: "复核", Kind: "research"},
		{Description: "发布", Kind: "edit"},
	}
	if p := run.PrefillOf(drifted); len(p) != 0 {
		t.Fatalf("drifted prefill = %+v", p)
	}
}

func TestRunPrune(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	for i := 0; i < maxRunsKept+5; i++ {
		if _, err := s.StartRun("", "ws", steps("a")); err != nil {
			t.Fatal(err)
		}
	}
	// 同拍序号保证 ID 唯一；目录里应只剩 maxRunsKept 条。
	entries, _ := os.ReadDir(filepath.Join(dir, runsDir))
	if len(entries) != maxRunsKept {
		t.Fatalf("kept %d, want %d", len(entries), maxRunsKept)
	}
}

func TestStepsOfRebuildsAfter(t *testing.T) {
	run := Run{Steps: []RunStep{
		{Index: 0, Name: "a", Kind: "research", Description: "扫描"},
		{Index: 1, Name: "b", Kind: "edit", Description: "修复", After: []string{"a"}},
	}}
	got := run.StepsOf()
	if len(got) != 2 || got[1].After[0] != "a" || got[1].Kind != "edit" {
		t.Fatalf("rebuild: %+v", got)
	}
}
