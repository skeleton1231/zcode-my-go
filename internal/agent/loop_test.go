package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// fakeProvider scripts assistant turns in order, one per Stream call.
type fakeProvider struct {
	turns []fakeTurn
	calls int
}

type fakeTurn struct {
	events []ai.StreamEvent
}

func (p *fakeProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	// steering 等场景会追加轮次：越界时重复最后一条脚本。
	idx := p.calls
	if idx >= len(p.turns) {
		idx = len(p.turns) - 1
	}
	out := make(chan ai.StreamEvent, len(p.turns[idx].events)+1)
	for _, ev := range p.turns[idx].events {
		out <- ev
	}
	p.calls++
	close(out)
	return out
}

// fakeTool records its executions.
type fakeTool struct {
	name  string
	runs  int
	last  json.RawMessage
	agent ToolResult
}

func (t *fakeTool) Name() string        { return t.name }
func (t *fakeTool) Description() string { return "fake" }
func (t *fakeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *fakeTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	t.runs++
	t.last = args
	return t.agent
}

func textTurn(text string, usage ai.Usage) fakeTurn {
	return fakeTurn{events: []ai.StreamEvent{
		{Kind: ai.EvMessageStart},
		{Kind: ai.EvTextDelta, TextDelta: text},
		{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn, Usage: usage},
	}}
}

func toolTurn(callID, name, args string) fakeTurn {
	return fakeTurn{events: []ai.StreamEvent{
		{Kind: ai.EvMessageStart},
		{Kind: ai.EvToolCallStart, ToolCallID: callID, ToolCallName: name},
		{Kind: ai.EvToolCallDelta, ToolCallArgs: args},
		{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
	}}
}

// collector gathers events and transcripts for assertions.
type collector struct {
	events   []Event
	messages []ai.Message
}

func (c *collector) emit(ev Event) { c.events = append(c.events, ev) }
func (c *collector) append(m ai.Message) error {
	c.messages = append(c.messages, m)
	return nil
}

func (c *collector) kinds() string {
	kinds := make([]string, len(c.events))
	for i, e := range c.events {
		kinds[i] = string(e.Kind)
	}
	return strings.Join(kinds, " ")
}

func runLoop(t *testing.T, provider ai.Provider, tools []Tool, messages []ai.Message) *collector {
	t.Helper()
	c := &collector{messages: messages}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Model:    "fake-model",
		Tools:    tools,
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)
	return c
}

// TestPlainTextTurn verifies the event sequence of a tool-free answer.
func TestPlainTextTurn(t *testing.T) {
	c := runLoop(t, &fakeProvider{turns: []fakeTurn{textTurn("hello", ai.Usage{InputTokens: 10, OutputTokens: 5})}}, nil, nil)

	want := "agent_start turn_start message_start text_delta message_end turn_end agent_end"
	if got := c.kinds(); got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if len(c.messages) != 1 || c.messages[0].Text() != "hello" {
		t.Fatalf("messages = %+v", c.messages)
	}
	last := c.events[len(c.events)-1]
	if last.Usage.OutputTokens != 5 || last.Usage.InputTokens != 10 {
		t.Fatalf("usage = %+v", last.Usage)
	}
}

// TestToolCallRound executes the tool, feeds the result back, and stops on
// the model's final answer.
func TestToolCallRound(t *testing.T) {
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "file body"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{"path":"a.txt"}`),
		textTurn("done", ai.Usage{}),
	}}
	c := runLoop(t, provider, []Tool{tool}, nil)

	if tool.runs != 1 {
		t.Fatalf("tool ran %d times, want 1", tool.runs)
	}
	if string(tool.last) != `{"path":"a.txt"}` {
		t.Fatalf("args = %s", tool.last)
	}
	if len(c.messages) != 3 {
		t.Fatalf("messages = %+v", c.messages)
	}
	result := c.messages[1]
	if result.Role != ai.RoleToolResult || len(result.Blocks) != 1 ||
		result.Blocks[0].ToolCallID != "c1" || result.Blocks[0].Text != "file body" {
		t.Fatalf("tool result message = %+v", result)
	}
	if !strings.Contains(c.kinds(), "tool_execution_start tool_execution_end") {
		t.Fatalf("missing tool execution events: %s", c.kinds())
	}
}

// TestTruncatedToolCallsAreFailed verifies the length-stop guard: tool calls
// from a truncated message are never executed, and the loop continues so
// the model can retry.
func TestTruncatedToolCallsAreFailed(t *testing.T) {
	tool := &fakeTool{name: "write"}
	provider := &fakeProvider{turns: []fakeTurn{{
		events: []ai.StreamEvent{
			{Kind: ai.EvMessageStart},
			{Kind: ai.EvToolCallStart, ToolCallID: "c1", ToolCallName: "write"},
			{Kind: ai.EvToolCallDelta, ToolCallArgs: `{"path":"a`}, // cut mid-argument
			{Kind: ai.EvMessageEnd, StopReason: ai.StopLength},
		},
	}, textTurn("recovered", ai.Usage{})}}
	c := runLoop(t, provider, []Tool{tool}, nil)

	if tool.runs != 0 {
		t.Fatalf("truncated call was executed %d times", tool.runs)
	}
	if len(c.messages) != 3 {
		t.Fatalf("messages = %+v", c.messages)
	}
	result := c.messages[1]
	if !result.Blocks[0].IsError {
		t.Fatalf("expected error tool result, got %+v", result)
	}
	if !strings.Contains(result.Blocks[0].Text, "token") {
		t.Fatalf("failure text = %q", result.Blocks[0].Text)
	}
}

// TestErrorStopsRun verifies provider failures end the run without tool
// execution.
func TestErrorStopsRun(t *testing.T) {
	tool := &fakeTool{name: "read"}
	provider := &fakeProvider{turns: []fakeTurn{{
		events: []ai.StreamEvent{
			{Kind: ai.EvMessageStart},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopError, ErrorMessage: "boom"},
		},
	}}}
	c := runLoop(t, provider, []Tool{tool}, nil)

	if tool.runs != 0 {
		t.Fatal("tool ran on error turn")
	}
	end := c.events[len(c.events)-1]
	if end.Kind != EvAgentEnd || end.StopReason != ai.StopError || end.ErrorMessage != "boom" {
		t.Fatalf("agent_end = %+v", end)
	}
}

// TestUnknownToolFailsSoftly verifies unknown tools become error results
// instead of crashing the loop.
func TestUnknownToolFailsSoftly(t *testing.T) {
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "nope", `{}`),
		textTurn("recovered", ai.Usage{}),
	}}
	c := runLoop(t, provider, nil, nil)

	result := c.messages[1]
	if !result.Blocks[0].IsError || !strings.Contains(result.Blocks[0].Text, "unknown tool") {
		t.Fatalf("result = %+v", result)
	}
}

// TestTurnLimitStopsLooping verifies the loop cannot run forever.
func TestTurnLimitStopsLooping(t *testing.T) {
	turns := make([]fakeTurn, 100)
	for i := range turns {
		turns[i] = toolTurn(fmt.Sprintf("c%d", i), "read", `{}`)
	}
	tool := &fakeTool{name: "read"}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: &fakeProvider{turns: turns},
		Tools:    []Tool{tool},
		MaxTurns: 3,
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)

	if len(c.messages) != 2*3 {
		t.Fatalf("appended %d messages, want %d", len(c.messages), 2*3)
	}
}

// TestHistorySnapshotPerTurn verifies the provider sees messages appended
// by the previous turn (user, assistant, tool results).
func TestHistorySnapshotPerTurn(t *testing.T) {
	var seen [][]ai.Message
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "x"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{}`),
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{messages: []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "go"}}},
	}}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{tool},
		History: func() []ai.Message {
			snapshot := append([]ai.Message(nil), c.messages...)
			seen = append(seen, snapshot)
			return snapshot
		},
	}, c.emit)
	loop.Run(context.Background(), c.append)

	if len(seen) != 2 {
		t.Fatalf("provider called %d times", len(seen))
	}
	if len(seen[0]) != 1 || seen[0][0].Role != ai.RoleUser {
		t.Fatalf("first call history = %+v", seen[0])
	}
	if len(seen[1]) != 3 || seen[1][2].Role != ai.RoleToolResult {
		t.Fatalf("second call history = %+v", seen[1])
	}
}
