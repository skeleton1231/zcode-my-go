package uiapp

import (
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func ev(kind agent.EventKind, mutate func(*agent.Event)) agent.Event {
	e := agent.Event{Kind: kind}
	if mutate != nil {
		mutate(&e)
	}
	return e
}

func TestChatStateTextTurn(t *testing.T) {
	var s chatState
	s.apply(ev(agent.EvAgentStart, nil))
	if !s.running {
		t.Fatal("not running after agent_start")
	}
	s.apply(ev(agent.EvMessageStart, nil))
	s.apply(ev(agent.EvTextDelta, func(e *agent.Event) { e.Text = "你好" }))
	s.apply(ev(agent.EvTextDelta, func(e *agent.Event) { e.Text = "，世界" }))
	if len(s.items) != 1 || s.items[0].text != "你好，世界" || !s.items[0].streaming {
		t.Fatalf("items = %+v", s.items)
	}
	s.apply(ev(agent.EvMessageEnd, func(e *agent.Event) {
		e.StopReason = ai.StopEndTurn
		e.Usage = ai.Usage{InputTokens: 10, OutputTokens: 5}
	}))
	s.apply(ev(agent.EvAgentEnd, func(e *agent.Event) { e.StopReason = ai.StopEndTurn }))
	if s.running || s.items[0].streaming {
		t.Fatalf("state = %+v", s)
	}
	if s.usage.InputTokens != 10 || !s.hasUsage {
		t.Fatalf("usage = %+v", s.usage)
	}
}

func TestChatStateErrorTurn(t *testing.T) {
	var s chatState
	s.apply(ev(agent.EvAgentStart, nil))
	s.apply(ev(agent.EvMessageStart, nil))
	s.apply(ev(agent.EvMessageEnd, func(e *agent.Event) {
		e.StopReason = ai.StopError
		e.ErrorMessage = "boom"
	}))
	s.apply(ev(agent.EvAgentEnd, func(e *agent.Event) {
		e.StopReason = ai.StopError
		e.ErrorMessage = "boom"
	}))
	if s.items[0].errMsg != "boom" || s.endErr != "boom" {
		t.Fatalf("state = %+v", s)
	}
}

func TestChatStateToolRound(t *testing.T) {
	var s chatState
	s.apply(ev(agent.EvAgentStart, nil))
	s.apply(ev(agent.EvToolExecutionStart, func(e *agent.Event) {
		e.ToolCallID = "c1"
		e.ToolName = "read"
		e.Args = `{"path":"a.go"}`
	}))
	if len(s.items) != 1 || !s.items[0].running || s.items[0].name != "read" {
		t.Fatalf("items = %+v", s.items)
	}
	s.apply(ev(agent.EvToolExecutionEnd, func(e *agent.Event) {
		e.ToolCallID = "c1"
		e.ResultContent = "contents"
	}))
	if s.items[0].running || s.items[0].result != "contents" {
		t.Fatalf("items = %+v", s.items)
	}
	// 多工具按 callId 定位。
	s.apply(ev(agent.EvToolExecutionStart, func(e *agent.Event) {
		e.ToolCallID = "b"
		e.ToolName = "grep"
	}))
	s.apply(ev(agent.EvToolExecutionEnd, func(e *agent.Event) { e.ToolCallID = "b" }))
	if s.items[1].name != "grep" || s.items[1].result != "" {
		t.Fatalf("items = %+v", s.items)
	}
}

func TestChatStateAgentEndCollapses(t *testing.T) {
	var s chatState
	s.apply(ev(agent.EvAgentStart, nil))
	s.apply(ev(agent.EvMessageStart, nil))
	s.apply(ev(agent.EvToolExecutionStart, func(e *agent.Event) { e.ToolCallID = "c1" }))
	s.apply(ev(agent.EvAgentEnd, func(e *agent.Event) { e.StopReason = ai.StopAborted }))
	for _, item := range s.items {
		if item.streaming || item.running {
			t.Fatalf("not collapsed: %+v", s.items)
		}
	}
	if s.running {
		t.Fatal("still running")
	}
}

func TestRestoreChat(t *testing.T) {
	s := restoreChat([]ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "fix"}}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockText, Text: "looking"},
			{Kind: ai.BlockToolCall, ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
		}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			{Kind: ai.BlockToolResult, ToolCallID: "c1", Text: "body"},
		}},
	})
	if len(s.items) != 3 {
		t.Fatalf("items = %+v", s.items)
	}
	tool := s.items[2]
	if tool.name != "read" || tool.result != "body" || tool.running {
		t.Fatalf("tool = %+v", tool)
	}

	// 截断的调用（无结果消息）保持 running 语义。
	s = restoreChat([]ai.Message{
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockToolCall, ID: "c9", Name: "write", Arguments: "{}"},
		}},
	})
	if !s.items[0].running {
		t.Fatalf("truncated call = %+v", s.items[0])
	}
}

func TestAppendUserAndUsage(t *testing.T) {
	var s chatState
	s.appendUser("hi")
	if len(s.items) != 1 || s.items[0].kind != itemUser || s.items[0].text != "hi" {
		t.Fatalf("items = %+v", s.items)
	}
	if got := formatUsage(s); got != "" {
		t.Fatalf("usage = %q", got)
	}
	s.apply(ev(agent.EvAgentEnd, func(e *agent.Event) {
		e.Usage = ai.Usage{InputTokens: 1500, OutputTokens: 700}
	}))
	if got := formatUsage(s); got != "↑1.5k ↓700" {
		t.Fatalf("usage = %q", got)
	}
}
