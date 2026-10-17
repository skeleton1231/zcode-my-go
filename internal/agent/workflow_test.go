package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
