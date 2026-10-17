package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func TestDispatchCollectsInOrder(t *testing.T) {
	var seen []string
	tool := NewDispatch(func(_ context.Context, prompts []string) []agent.SubtaskResult {
		out := make([]agent.SubtaskResult, len(prompts))
		for i, p := range prompts {
			seen = append(seen, p)
			out[i] = agent.SubtaskResult{Text: "回答 " + p}
		}
		return out
	})
	res := tool.Execute(context.Background(), json.RawMessage(
		`{"prompts":["甲的问题","乙的问题"]}`))
	if res.IsError {
		t.Fatalf("dispatch: %s", res.Content)
	}
	for _, want := range []string{"任务 1", "回答 甲的问题", "任务 2", "回答 乙的问题"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in %q", want, res.Content)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("runner saw %d prompts", len(seen))
	}
}

func TestDispatchValidation(t *testing.T) {
	tool := NewDispatch(nil)
	if res := tool.Execute(context.Background(), json.RawMessage(`{"prompts":["only one"]}`)); !res.IsError {
		t.Fatal("single prompt accepted")
	}
	if res := tool.Execute(context.Background(), json.RawMessage(
		`{"prompts":["a","b","c","d","e"]}`)); !res.IsError {
		t.Fatal("five prompts accepted")
	}
	if res := tool.Execute(context.Background(), json.RawMessage(`{"prompts":["a",""]}`)); !res.IsError {
		t.Fatal("empty prompt accepted")
	}
	if res := NewDispatch(nil).Execute(context.Background(), json.RawMessage(`{"prompts":["a","b"]}`)); !res.IsError {
		t.Fatal("nil runner accepted")
	}
}

// TestDispatchOutputCapped 验证每份子报告按 task 同型截断（issue #92：
// 4 份无限长回答直接拼接，一次性进主对话上下文）。
func TestDispatchOutputCapped(t *testing.T) {
	big := strings.Repeat("报", 200*1024) // ~400KB/份
	tool := NewDispatch(func(_ context.Context, prompts []string) []agent.SubtaskResult {
		out := make([]agent.SubtaskResult, len(prompts))
		for i := range prompts {
			out[i] = agent.SubtaskResult{Text: big, Turns: 2}
		}
		return out
	})
	res := tool.Execute(context.Background(), json.RawMessage(
		`{"prompts":["a","b","c","d"]}`))
	if len(res.Content) > 4*(2*taskHeadTail+512) {
		t.Fatalf("dispatch output = %d bytes, want capped per report", len(res.Content))
	}
	if !strings.Contains(res.Content, "bytes truncated") {
		t.Fatal("truncation marker missing")
	}
}
