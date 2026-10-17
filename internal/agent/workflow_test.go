package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// scriptedProvider answers every turn with the same shape, per script.
type scriptedProvider struct{ replies []string }

func (p *scriptedProvider) Stream(_ context.Context, _ ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: p.replies[0]}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// TestRunWorkflowExecutesStepsInOrder verifies the plan brief, step kinds
// and ordered results.
func TestRunWorkflowExecutesStepsInOrder(t *testing.T) {
	var seen []string
	research := &recordingTool{name: "read"}
	editable := &recordingTool{name: "write"}
	provider := &promptEchoProvider{record: &seen}
	res, err := RunWorkflow(context.Background(), WorkflowConfig{
		Provider: provider,
		Model:    "m",
		ReadOnly: []Tool{research},
		Editable: []Tool{editable},
	}, []WorkflowStep{
		{Description: "调查结构", Kind: StepResearch},
		{Description: "写入文件", Kind: StepEdit},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Failed || res[1].Failed {
		t.Fatalf("results = %+v", res)
	}
	// 每一步的提示包含计划与本步指派。
	if !strings.Contains(seen[0], "▶ 1. [research] 调查结构") {
		t.Fatalf("step-1 brief = %q", seen[0])
	}
	if !strings.Contains(seen[1], "调查结构") || !strings.Contains(seen[1], "▶ 2. [edit] 写入文件") {
		t.Fatalf("step-2 brief misses prior results: %q", seen[1])
	}
}

// TestRunWorkflowEditStepsPassGate verifies edit steps inherit the
// permission gate: a denial stops the workflow with a partial report.
func TestRunWorkflowEditStepsPassGate(t *testing.T) {
	editable := &recordingTool{name: "write"}
	perms := permission.New(permission.Ruleset{})
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "write", "{}"),
		textTurn("done", ai.Usage{}),
	}}
	results, err := RunWorkflow(context.Background(), WorkflowConfig{
		Provider:    provider,
		Model:       "m",
		ReadOnly:    []Tool{},
		Editable:    []Tool{editable},
		Permissions: perms,
	}, []WorkflowStep{
		{Description: "改文件", Kind: StepEdit},
	})
	// 权限拒绝不等于步骤崩溃：子代理会收到拒绝结果并汇报，
	// 关键是被拒的工具从未执行。
	if err != nil {
		t.Fatalf("step errored: %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("results = %+v", results)
	}
	if editable.runs != 0 {
		t.Fatal("denied step executed")
	}
}

// TestRunWorkflowValidatesSteps verifies step validation.
func TestRunWorkflowValidatesSteps(t *testing.T) {
	if _, err := RunWorkflow(context.Background(), WorkflowConfig{}, nil); err == nil {
		t.Fatal("empty workflow accepted")
	}
	if _, err := RunWorkflow(context.Background(), WorkflowConfig{},
		[]WorkflowStep{{Description: "x", Kind: "magic"}}); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

// recordingTool records runs.
type recordingTool struct {
	name string
	runs int
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return "recording" }
func (t *recordingTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *recordingTool) Execute(context.Context, json.RawMessage) ToolResult {
	t.runs++
	return ToolResult{Content: "ran"}
}

// promptEchoProvider answers with the prompt it was given.
type promptEchoProvider struct {
	record *[]string
}

func (p *promptEchoProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	*p.record = append(*p.record, req.Messages[len(req.Messages)-1].Text())
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "step done"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// gateProvider proves concurrency: every Stream registers as in-flight and
// holds until `threshold` Streams are alive at once (a timeout then lets a
// sequential run finish, so the peak assertion fails honestly instead of
// hanging). Replies echo the step's assignment so join steps carry
// identifiable upstream results.
type gateProvider struct {
	threshold int

	mu       sync.Mutex
	prompts  []string
	inflight int
	peak     int
	overlap  chan struct{}
	once     sync.Once
}

func newGateProvider(threshold int) *gateProvider {
	return &gateProvider{threshold: threshold, overlap: make(chan struct{})}
}

func (p *gateProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	prompt := req.Messages[len(req.Messages)-1].Text()
	p.mu.Lock()
	p.prompts = append(p.prompts, prompt)
	p.inflight++
	if p.inflight > p.peak {
		p.peak = p.inflight
	}
	hit := p.peak >= p.threshold
	p.mu.Unlock()
	if hit {
		p.once.Do(func() { close(p.overlap) })
	}
	out := make(chan ai.StreamEvent, 3)
	go func() {
		if !hit {
			select {
			case <-p.overlap:
			case <-time.After(2 * time.Second):
			}
		}
		p.mu.Lock()
		p.inflight--
		p.mu.Unlock()
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "完成: " + assignment(prompt)}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		close(out)
	}()
	return out
}

func (p *gateProvider) peakInflight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

func (p *gateProvider) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.prompts...)
}

// assignment extracts the current step's assignment line from a brief.
func assignment(prompt string) string {
	if i := strings.Index(prompt, "现在执行第"); i >= 0 {
		return prompt[i:]
	}
	return prompt
}

// TestRunWorkflowDAGRunsIndependentStepsInParallel: A and B (no
// dependencies) must overlap in flight; C waits for both and its brief
// carries both upstream results.
func TestRunWorkflowDAGRunsIndependentStepsInParallel(t *testing.T) {
	provider := newGateProvider(2)
	res, err := RunWorkflow(context.Background(), WorkflowConfig{
		Provider: provider,
		Model:    "m",
	}, []WorkflowStep{
		{Name: "A", Description: "调查甲", Kind: StepResearch},
		{Name: "B", Description: "调查乙", Kind: StepResearch},
		{Name: "C", Description: "汇总", Kind: StepResearch, After: []string{"A", "B"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].Failed || res[1].Failed || res[2].Failed {
		t.Fatalf("results = %+v", res)
	}
	if peak := provider.peakInflight(); peak < 2 {
		t.Fatalf("A and B never overlapped (peak=%d): steps ran sequentially", peak)
	}
	prompts := provider.recorded()
	if len(prompts) != 3 {
		t.Fatalf("prompts = %q", prompts)
	}
	if !strings.Contains(prompts[2], "A: 完成:") || !strings.Contains(prompts[2], "B: 完成:") {
		t.Fatalf("join step misses upstream results: %q", prompts[2])
	}
}

// TestRunWorkflowDAGValidatesGraph: name/addressing errors are rejected
// before any step runs.
func TestRunWorkflowDAGValidatesGraph(t *testing.T) {
	unknown := "unknown step \"c\""
	cases := []struct {
		name  string
		steps []WorkflowStep
		want  string
	}{
		{"missing name", []WorkflowStep{
			{Name: "a", Description: "x", Kind: StepResearch},
			{Description: "y", Kind: StepResearch, After: []string{"a"}},
		}, "lacks a name"},
		{"duplicate name", []WorkflowStep{
			{Name: "a", Description: "x", Kind: StepResearch},
			{Name: "b", Description: "y", Kind: StepResearch},
			{Name: "a", Description: "z", Kind: StepResearch, After: []string{"b"}},
		}, "duplicate step name"},
		{"unknown reference", []WorkflowStep{
			{Name: "a", Description: "x", Kind: StepResearch},
			{Name: "b", Description: "y", Kind: StepResearch, After: []string{"c"}},
		}, unknown},
		{"self dependency", []WorkflowStep{
			{Name: "a", Description: "x", Kind: StepResearch, After: []string{"a"}},
		}, "dependency cycle"},
		{"cycle", []WorkflowStep{
			{Name: "a", Description: "x", Kind: StepResearch, After: []string{"c"}},
			{Name: "b", Description: "y", Kind: StepResearch, After: []string{"a"}},
			{Name: "c", Description: "z", Kind: StepResearch, After: []string{"b"}},
		}, "dependency cycle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunWorkflow(context.Background(), WorkflowConfig{}, tc.steps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// failingProvider fails subtasks whose brief carries the marker.
type failingProvider struct{ marker string }

func (p *failingProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	prompt := req.Messages[len(req.Messages)-1].Text()
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	if strings.Contains(prompt, p.marker) {
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError, ErrorMessage: "注入失败"}
		close(out)
		return out
	}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "完成"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// TestRunWorkflowDAGFailureSkipsDependents: B's failure stops new
// dispatches; independent A and D still complete, dependent C is reported
// as skipped and never attempted.
func TestRunWorkflowDAGFailureSkipsDependents(t *testing.T) {
	res, err := RunWorkflow(context.Background(), WorkflowConfig{
		Provider: &failingProvider{marker: "必败"},
		Model:    "m",
	}, []WorkflowStep{
		{Name: "A", Description: "正常甲", Kind: StepResearch},
		{Name: "B", Description: "必败乙", Kind: StepResearch},
		{Name: "C", Description: "依赖B", Kind: StepResearch, After: []string{"B"}},
		{Name: "D", Description: "正常丁", Kind: StepResearch},
	})
	if err == nil {
		t.Fatal("failure swallowed")
	}
	if !strings.Contains(err.Error(), "skipped 1 step(s): C") {
		t.Fatalf("err = %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("results = %+v", res)
	}
	for _, r := range res {
		if r.Step.Name == "C" {
			t.Fatal("skipped step reported as attempted")
		}
		if r.Step.Name == "B" && !r.Failed {
			t.Fatal("failed step not marked")
		}
	}
}

// TestRunWorkflowDAGConcurrencyCap: six steps (one After, so the DAG path
// runs — without any After the plan is sequential by design), at most
// MaxParallelWorkflowSteps in flight — and the cap is actually reached.
func TestRunWorkflowDAGConcurrencyCap(t *testing.T) {
	provider := newGateProvider(MaxParallelWorkflowSteps)
	steps := make([]WorkflowStep, 6)
	for i := range steps {
		steps[i] = WorkflowStep{
			Name:        fmt.Sprintf("s%d", i),
			Description: fmt.Sprintf("步骤%d", i),
			Kind:        StepResearch,
		}
	}
	steps[1].After = []string{"s0"}
	res, err := RunWorkflow(context.Background(), WorkflowConfig{
		Provider: provider,
		Model:    "m",
	}, steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 6 {
		t.Fatalf("results = %d steps", len(res))
	}
	if peak := provider.peakInflight(); peak != MaxParallelWorkflowSteps {
		t.Fatalf("peak = %d, want the cap %d", peak, MaxParallelWorkflowSteps)
	}
}

// TestRunWorkflowDAGEditStepsPassGate: edit steps in a DAG inherit the
// permission gate — the denied tool never runs, the step itself reports.
func TestRunWorkflowDAGEditStepsPassGate(t *testing.T) {
	editable := &recordingTool{name: "write"}
	perms := permission.New(permission.Ruleset{})
	provider := &fakeProvider{turns: []fakeTurn{
		textTurn("查完", ai.Usage{}),
		toolTurn("c1", "write", "{}"),
		textTurn("done", ai.Usage{}),
	}}
	results, err := RunWorkflow(context.Background(), WorkflowConfig{
		Provider:    provider,
		Model:       "m",
		ReadOnly:    []Tool{},
		Editable:    []Tool{editable},
		Permissions: perms,
	}, []WorkflowStep{
		{Name: "r", Description: "调查", Kind: StepResearch},
		{Name: "e", Description: "改文件", Kind: StepEdit, After: []string{"r"}},
	})
	// 权限拒绝不等于步骤崩溃：子代理会收到拒绝结果并汇报，
	// 关键是被拒的工具从未执行（同串行语义）。
	if err != nil {
		t.Fatalf("step errored: %v", err)
	}
	if len(results) != 2 || results[1].Failed {
		t.Fatalf("results = %+v", results)
	}
	if editable.runs != 0 {
		t.Fatal("denied step executed")
	}
}

// TestTruncateRuneSafe 验证步骤结果截断按 rune（issue #99：字节截断
// 让中文尾部乱码进子代理 prompt）。
func TestTruncateRuneSafe(t *testing.T) {
	s := strings.Repeat("项", 300) // 900 字节
	got := truncate(s, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got[len(got)-12:])
	}
	if n := len([]rune(got)); n != 101 { // 100 rune + 省略号
		t.Fatalf("runes = %d", n)
	}
	if truncate("短", 10) != "短" {
		t.Fatal("short string mangled")
	}
}
