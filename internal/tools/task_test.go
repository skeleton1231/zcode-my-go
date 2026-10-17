package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestTaskOutputCapped 验证子代理报告被截断（issue #21：task 曾是唯一
// 无输出上限的工具，一次失控调用吃掉数万 token）。
func TestTaskOutputCapped(t *testing.T) {
	big := strings.Repeat("报", 100*1024) // ~200KB
	task := NewTask(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: big, Turns: 3}, nil
	})
	res := task.Execute(context.Background(), []byte(`{"prompt":"探索"}`))
	if len(res.Content) > 2*taskHeadTail+512 {
		t.Fatalf("task output = %d bytes, want capped", len(res.Content))
	}
	if !strings.Contains(res.Content, "bytes truncated") {
		t.Fatalf("truncation marker missing: %q…", res.Content[:80])
	}
	if !strings.Contains(res.Content, "3 轮") {
		t.Fatal("subagent turn count lost")
	}
}

// TestTaskSmallOutputIntact 验证小报告原样保留。
func TestTaskSmallOutputIntact(t *testing.T) {
	task := NewTask(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: "结论：一切正常。", Turns: 1}, nil
	})
	res := task.Execute(context.Background(), []byte(`{"prompt":"看一眼"}`))
	if !strings.Contains(res.Content, "结论：一切正常。") || strings.Contains(res.Content, "truncated") {
		t.Fatalf("small output mangled: %q", res.Content)
	}
}
