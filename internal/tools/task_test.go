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
	task := NewTask(func(ctx context.Context, prompt, agentName string) (agent.SubtaskResult, error) {
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
	task := NewTask(func(ctx context.Context, prompt, agentName string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: "结论：一切正常。", Turns: 1}, nil
	})
	res := task.Execute(context.Background(), []byte(`{"prompt":"看一眼"}`))
	if !strings.Contains(res.Content, "结论：一切正常。") || strings.Contains(res.Content, "truncated") {
		t.Fatalf("small output mangled: %q", res.Content)
	}
}

// TestTaskAgentParam 验证 agent 参数透传给 runner（spec 规则 51：命中
// 定义走其 persona，未命中由 runner 报错自纠），以及名字含路径/空白的
// 拦截在工具层完成。
func TestTaskAgentParam(t *testing.T) {
	var gotName string
	task := NewTask(func(ctx context.Context, prompt, agentName string) (agent.SubtaskResult, error) {
		gotName = agentName
		if agentName == "ghost" {
			return agent.SubtaskResult{}, context.Canceled
		}
		return agent.SubtaskResult{Text: "ok"}, nil
	})
	res := task.Execute(context.Background(), []byte(`{"prompt":"查","agent":"reviewer"}`))
	if res.IsError || gotName != "reviewer" {
		t.Fatalf("agent passthrough: name=%q res=%+v", gotName, res)
	}
	if res := task.Execute(context.Background(), []byte(`{"prompt":"查"}`)); res.IsError || gotName != "" {
		t.Fatalf("default path broken: name=%q", gotName)
	}
	if res := task.Execute(context.Background(), []byte(`{"prompt":"查","agent":"a/b"}`)); !res.IsError {
		t.Fatal("path-like agent name accepted")
	}
	if res := task.Execute(context.Background(), []byte(`{"prompt":"查","agent":"ghost"}`)); !res.IsError {
		t.Fatal("runner error swallowed")
	}
}
