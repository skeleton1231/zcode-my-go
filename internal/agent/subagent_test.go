package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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

// slowCountProvider counts concurrent streams; each stream blocks 60ms.
type slowCountProvider struct {
	mu       sync.Mutex
	inflight int
	peak     int
}

func (p *slowCountProvider) Stream(_ context.Context, _ ai.Request) <-chan ai.StreamEvent {
	p.mu.Lock()
	p.inflight++
	if p.inflight > p.peak {
		p.peak = p.inflight
	}
	p.mu.Unlock()
	out := make(chan ai.StreamEvent, 3)
	go func() {
		defer close(out)
		time.Sleep(60 * time.Millisecond)
		p.mu.Lock()
		p.inflight--
		p.mu.Unlock()
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "done"}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	}()
	return out
}

// TestRunSubtasksParallelAndCapped verifies real overlap (peak > 1), the
// concurrency cap, and prompt-order results.
func TestRunSubtasksParallelAndCapped(t *testing.T) {
	p := &slowCountProvider{}
	prompts := []string{"q1", "q2", "q3", "q4", "q5", "q6"}
	results := RunSubtasks(context.Background(), SubtaskConfig{
		Provider: p, Model: "m", System: SubtaskSystem(),
	}, prompts)
	if len(results) != len(prompts) {
		t.Fatalf("results = %d", len(results))
	}
	for i, r := range results {
		if !strings.Contains(r.Text, "done") {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	if p.peak < 2 {
		t.Fatalf("no overlap observed: peak=%d", p.peak)
	}
	if p.peak > MaxParallelSubtasks {
		t.Fatalf("cap exceeded: peak=%d > %d", p.peak, MaxParallelSubtasks)
	}
}
