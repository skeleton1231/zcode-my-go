package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestRunSubtaskRunsNestedLoop verifies a subagent explores with its tools
// and returns the final answer with a turn count.
func TestRunSubtaskRunsNestedLoop(t *testing.T) {
	probe := &fakeTool{name: "read", agent: ToolResult{Content: "source"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{"path":"a.go"}`),
		textTurn("答案在 a.go 的 Foo()", ai.Usage{}),
	}}
	res, err := RunSubtask(context.Background(), SubtaskConfig{
		Provider: provider,
		Model:    "m",
		System:   SubtaskSystem(),
		Tools:    []Tool{probe},
	}, "Foo 在哪里定义？")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Foo") {
		t.Fatalf("answer = %q", res.Text)
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2", res.Turns)
	}
	if probe.runs != 1 {
		t.Fatalf("probe runs = %d", probe.runs)
	}
}

// TestRunSubtaskRejectsEmptyPrompt verifies the misuse guard.
func TestRunSubtaskRejectsEmptyPrompt(t *testing.T) {
	if _, err := RunSubtask(context.Background(), SubtaskConfig{}, "  "); err == nil {
		t.Fatal("empty prompt accepted")
	}
}
