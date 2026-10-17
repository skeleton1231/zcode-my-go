package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// gatedTool declares a capability like a real tool would.
type gatedTool struct {
	name string
	cap  permission.Capability
	runs int
}

func (t *gatedTool) Name() string        { return t.name }
func (t *gatedTool) Description() string { return "gated" }
func (t *gatedTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *gatedTool) Execute(context.Context, json.RawMessage) ToolResult {
	t.runs++
	return ToolResult{Content: "ran"}
}
func (t *gatedTool) Capability() permission.Capability { return t.cap }

func permissionTurn(t *testing.T, tool Tool) fakeTurn {
	t.Helper()
	return toolTurn("c1", tool.Name(), `{}`)
}

// TestPermissionGateBlocksWhenDenied verifies an ask the user refuses never
// reaches the tool, and the model sees a structured denial.
func TestPermissionGateBlocksWhenDenied(t *testing.T) {
	tool := &gatedTool{name: "write", cap: permission.Capability{}}
	provider := &fakeProvider{turns: []fakeTurn{
		permissionTurn(t, tool),
		textTurn("recovered", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider:    provider,
		Tools:       []Tool{tool},
		Permissions: permission.New(permission.Ruleset{}),
		Ask: func(AskRequest) AskResponse {
			return AskResponse{Deny: true, Note: "先别动这个文件"}
		},
		History: func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)

	if tool.runs != 0 {
		t.Fatalf("denied call executed %d times", tool.runs)
	}
	result := c.messages[1]
	if !result.Blocks[0].IsError ||
		!strings.Contains(result.Blocks[0].Text, "用户拒绝") ||
		!strings.Contains(result.Blocks[0].Text, "先别动这个文件") {
		t.Fatalf("denial result = %+v", result.Blocks[0])
	}
}

// TestPermissionGateAlwaysGrantsRule verifies an "always" answer stores the
// session rule and later same-shaped calls run without asking.
func TestPermissionGateAlwaysGrantsRule(t *testing.T) {
	tool := &gatedTool{name: "write", cap: permission.Capability{}}
	asked := 0
	perms := permission.New(permission.Ruleset{})
	c := &collector{}
	loopFor := func() {
		provider := &fakeProvider{turns: []fakeTurn{
			permissionTurn(t, tool),
			textTurn("done", ai.Usage{}),
		}}
		c2 := c
		loop := NewLoop(LoopConfig{
			Provider: provider, Tools: []Tool{tool}, Permissions: perms,
			Ask: func(AskRequest) AskResponse {
				asked++
				return AskResponse{Always: true}
			},
			History: func() []ai.Message { return c2.messages },
		}, c2.emit)
		loop.Run(context.Background(), c2.append)
	}
	loopFor()
	if tool.runs != 1 || asked != 1 {
		t.Fatalf("runs=%d asked=%d", tool.runs, asked)
	}
	loopFor()
	if tool.runs != 2 || asked != 1 {
		t.Fatalf("second run asked again: runs=%d asked=%d", tool.runs, asked)
	}
}

// TestPermissionGateReadOnlySkipsAsk verifies declared read-only tools run
// without a single ask.
func TestPermissionGateReadOnlySkipsAsk(t *testing.T) {
	tool := &gatedTool{name: "read", cap: permission.Capability{ReadOnly: true}}
	provider := &fakeProvider{turns: []fakeTurn{
		permissionTurn(t, tool),
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	asked := 0
	loop := NewLoop(LoopConfig{
		Provider:    provider,
		Tools:       []Tool{tool},
		Permissions: permission.New(permission.Ruleset{}),
		Ask: func(AskRequest) AskResponse {
			asked++
			return AskResponse{}
		},
		History: func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)

	if tool.runs != 1 || asked != 0 {
		t.Fatalf("read-only asked: runs=%d asked=%d", tool.runs, asked)
	}
}

// TestPermissionGateNilPermissionsBypasses verifies the compatibility path.
func TestPermissionGateNilPermissionsBypasses(t *testing.T) {
	tool := &gatedTool{name: "write", cap: permission.Capability{}}
	provider := &fakeProvider{turns: []fakeTurn{
		permissionTurn(t, tool),
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{tool},
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)
	if tool.runs != 1 {
		t.Fatalf("nil permissions blocked the call: runs=%d", tool.runs)
	}
}

// TestPermissionGateNoChannelDenies verifies asks without an approval
// channel stay on the safe side.
func TestPermissionGateNoChannelDenies(t *testing.T) {
	tool := &gatedTool{name: "write", cap: permission.Capability{}}
	provider := &fakeProvider{turns: []fakeTurn{
		permissionTurn(t, tool),
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider:    provider,
		Tools:       []Tool{tool},
		Permissions: permission.New(permission.Ruleset{}),
		History:     func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)
	if tool.runs != 0 {
		t.Fatal("no channel still ran the tool")
	}
}

// TestSummarizeArgs verifies the canonical line rules match against.
func TestSummarizeArgs(t *testing.T) {
	cases := []struct {
		args string
		want string
	}{
		{`{"path":"src/a.go"}`, "path: src/a.go"},
		{`{"command":"git status"}`, "command: git status"},
		{`{"path":"a.go","offset":10}`, "offset: 10 path: a.go"},
		{``, ""},
		{`{}`, ""},
		{`[1,2]`, "[1,2]"},
	}
	for _, c := range cases {
		var raw json.RawMessage
		if c.args != "" {
			raw = json.RawMessage(c.args)
		}
		if got := SummarizeArgs(raw); got != c.want {
			t.Errorf("SummarizeArgs(%s) = %q, want %q", c.args, got, c.want)
		}
	}
}

// retryingProvider fails N turns with a retryable error, then succeeds.
type retryingProvider struct {
	failures int
	replies  []fakeTurn
	calls    int
}

func (p *retryingProvider) Stream(_ context.Context, _ ai.Request) <-chan ai.StreamEvent {
	p.calls++
	out := make(chan ai.StreamEvent, 3)
	if p.calls <= p.failures {
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError,
			ErrorMessage: "429 Too Many Requests", Retryable: true, RetryAfterMS: 1}
		close(out)
		return out
	}
	for _, ev := range p.replies[0].events {
		out <- ev
	}
	close(out)
	return out
}

// TestLoopRetriesTransientFailures verifies a retryable turn is retried
// without entering the transcript, and the good answer lands.
func TestLoopRetriesTransientFailures(t *testing.T) {
	provider := &retryingProvider{failures: 2, replies: []fakeTurn{
		textTurn("recovered", ai.Usage{}),
	}}
	c := &collector{}
	var retries int
	emit := func(ev Event) {
		if ev.Kind == EvTurnRetry {
			retries++
		}
		c.emit(ev)
	}
	loop := NewLoop(LoopConfig{
		Provider:     provider,
		Tools:        nil,
		History:      func() []ai.Message { return c.messages },
		RetryBackoff: time.Millisecond,
	}, emit)
	loop.Run(context.Background(), c.append)

	if retries != 2 {
		t.Fatalf("retries = %d, want 2", retries)
	}
	if len(c.messages) != 1 || c.messages[0].Text() != "recovered" {
		t.Fatalf("messages = %+v", c.messages)
	}
	end := c.events[len(c.events)-1]
	if end.StopReason != ai.StopEndTurn {
		t.Fatalf("agent_end = %+v", end)
	}
}

// TestLoopStopsAfterRetryBudget verifies the budget caps the retries.
func TestLoopStopsAfterRetryBudget(t *testing.T) {
	provider := &retryingProvider{failures: 99, replies: nil}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider:     provider,
		History:      func() []ai.Message { return c.messages },
		RetryBackoff: time.Millisecond,
	}, c.emit)
	stop := loop.Run(context.Background(), c.append)
	if stop != ai.StopError {
		t.Fatalf("stop = %v", stop)
	}
	if provider.calls != DefaultMaxRetries+1 {
		t.Fatalf("calls = %d, want %d", provider.calls, DefaultMaxRetries+1)
	}
}

// TestLoopDoesNotRetryPermanentErrors verifies non-retryable failures end
// the run immediately.
func TestLoopDoesNotRetryPermanentErrors(t *testing.T) {
	provider := &fakeProvider{turns: []fakeTurn{{
		events: []ai.StreamEvent{
			{Kind: ai.EvMessageEnd, StopReason: ai.StopError, ErrorMessage: "401 unauthorized"},
		},
	}}}
	// fakeProvider pops per call; a second call would panic — retries would
	// fail the test loudly, which is the assertion.
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	stop := loop.Run(context.Background(), c.append)
	if stop != ai.StopError {
		t.Fatalf("stop = %v", stop)
	}
	if provider.calls != 1 {
		t.Fatalf("calls = %d, want 1", provider.calls)
	}
}
