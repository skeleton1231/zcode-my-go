package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
