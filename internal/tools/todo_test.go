package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func runTool(t *testing.T, tool interface {
	Execute(context.Context, json.RawMessage) agent.ToolResult
}, args string) agent.ToolResult {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(args))
}

// TestTodoWriteAndRead verifies the replace-wholesale semantics and the
// render format.
func TestTodoWriteAndRead(t *testing.T) {
	todo := NewTodo(NewStates().Todo)
	res := runTool(t, todo, `{"op":"write","tasks":[
		{"content":"探索代码","status":"done"},
		{"content":"写实现","status":"in_progress"},
		{"content":"补测试","status":"pending"}]}`)
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	for _, want := range []string{"✓ 1. 探索代码", "▶ 2. 写实现", "3. 补测试", "1/3 done"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("render missing %q:\n%s", want, res.Content)
		}
	}

	res = runTool(t, todo, `{"op":"read"}`)
	if res.IsError || !strings.Contains(res.Content, "写实现") {
		t.Fatalf("read = %+v", res)
	}

	res = runTool(t, todo, `{"op":"write","tasks":[]}`)
	if res.IsError || !strings.Contains(res.Content, "(no tasks)") {
		t.Fatalf("clear = %+v", res)
	}
}

// TestTodoStateSurvivesToolRebuild verifies the plan outlives the per-turn
// tool reconstruction — the reason States is session-scoped.
func TestTodoStateSurvivesToolRebuild(t *testing.T) {
	state := NewStates()
	runTool(t, NewTodo(state.Todo), `{"op":"write","tasks":[{"content":"x","status":"pending"}]}`)
	fresh := NewTodo(state.Todo) // 模拟下一回合重建工具
	res := runTool(t, fresh, `{"op":"read"}`)
	if !strings.Contains(res.Content, "x") {
		t.Fatalf("state lost: %s", res.Content)
	}
}

func TestTodoRejectsBadOps(t *testing.T) {
	todo := NewTodo(NewStates().Todo)
	if res := runTool(t, todo, `{"op":"delete"}`); !res.IsError {
		t.Fatal("unknown op accepted")
	}
	if res := runTool(t, todo, `{"op":"write"}`); !res.IsError {
		t.Fatal("write without tasks accepted")
	}
}

// TestAskDeliversQuestions verifies the ask tool routes through its
// callback and surfaces the answer.
func TestAskDeliversQuestions(t *testing.T) {
	var got agent.Question
	ask := NewAsk(func(_ context.Context, q agent.Question) (string, error) {
		got = q
		return "选 B", nil
	})
	res := runTool(t, ask, `{"question":"用哪个方案？","options":["A","B"]}`)
	if res.IsError {
		t.Fatalf("ask failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "用户回答：选 B") {
		t.Fatalf("answer missing: %s", res.Content)
	}
	if got.Text != "用哪个方案？" || len(got.Options) != 2 {
		t.Fatalf("delivered question = %+v", got)
	}
}

func TestAskWithoutChannel(t *testing.T) {
	res := runTool(t, NewAsk(nil), `{"question":"在吗"}`)
	if !res.IsError || !strings.Contains(res.Content, "no user channel") {
		t.Fatalf("nil channel accepted: %+v", res)
	}
}
