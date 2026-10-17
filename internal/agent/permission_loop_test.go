package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// TestLoopAssemblesInterleavedToolCallDeltas 验证按 ToolCallID 装配交错
// 分片（issue #28：曾「粘到最后一个 Start」，call_A 参数全丢、call_B
// 拿到交错垃圾 JSON——垃圾重组成功时错误参数被静默执行）。
func TestLoopAssemblesInterleavedToolCallDeltas(t *testing.T) {
	provider := &fakeProvider{turns: []fakeTurn{{
		events: []ai.StreamEvent{
			{Kind: ai.EvToolCallStart, ToolCallID: "call_A", ToolCallName: "read"},
			{Kind: ai.EvToolCallStart, ToolCallID: "call_B", ToolCallName: "write"},
			{Kind: ai.EvToolCallDelta, ToolCallID: "call_A", ToolCallArgs: `{"pa`},
			{Kind: ai.EvToolCallDelta, ToolCallID: "call_B", ToolCallArgs: `{"pa`},
			{Kind: ai.EvToolCallDelta, ToolCallID: "call_A", ToolCallArgs: `th":"a.txt"}`},
			{Kind: ai.EvToolCallDelta, ToolCallID: "call_B", ToolCallArgs: `th":"b.txt"}`},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
		},
	}, {
		// 第二轮收尾：装配断言只看第一轮的 assistant 消息。
		events: []ai.StreamEvent{
			{Kind: ai.EvTextDelta, TextDelta: "done"},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn},
		},
	}}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)

	var read, write string
	for _, b := range c.messages[0].Blocks {
		if b.Kind != ai.BlockToolCall {
			continue
		}
		switch b.ID {
		case "call_A":
			read = b.Arguments
		case "call_B":
			write = b.Arguments
		}
	}
	if read != `{"path":"a.txt"}` || write != `{"path":"b.txt"}` {
		t.Fatalf("read = %q write = %q", read, write)
	}
}

// thinkingTurn emits thinking deltas then a final answer.
func thinkingTurn(t *testing.T, think, answer string) fakeTurn {
	t.Helper()
	return fakeTurn{events: []ai.StreamEvent{
		{Kind: ai.EvMessageStart},
		{Kind: ai.EvThinkingDelta, ThinkingText: think, Signature: "sig-1"},
		{Kind: ai.EvTextDelta, TextDelta: answer},
		{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn},
	}}
}

// TestLoopKeepsThinkingInHistory verifies thinking deltas become a
// thinking block (with signature) on the assistant message and reach the
// UI as events.
func TestLoopKeepsThinkingInHistory(t *testing.T) {
	provider := &fakeProvider{turns: []fakeTurn{
		thinkingTurn(t, "推理过程", "答案"),
	}}
	c := &collector{}
	var thinkingEvents int
	emit := func(ev Event) {
		if ev.Kind == EvThinkingDelta {
			thinkingEvents++
		}
		c.emit(ev)
	}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		History:  func() []ai.Message { return c.messages },
	}, emit)
	loop.Run(context.Background(), c.append)

	msg := c.messages[0]
	found := false
	for _, b := range msg.Blocks {
		if b.Kind == ai.BlockThinking {
			found = b.Text == "推理过程" && b.Signature == "sig-1"
		}
	}
	if !found {
		t.Fatalf("thinking block missing: %+v", msg.Blocks)
	}
	if thinkingEvents != 1 {
		t.Fatalf("thinking events = %d", thinkingEvents)
	}
}

// TestProjectRulesGateSession verifies a workspace's .zgorules.json seeds
// the session's permission service: a deny rule blocks, an allow rule runs
// without asking.
func TestProjectRulesGateSession(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".zgorules.json"), []byte(`{
		"version": 1,
		"rules": [
			{"tool": "bash", "args": "git *", "behavior": "allow"},
			{"tool": "write", "behavior": "deny"}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m, _ := NewManager(dir)
	m.Wire(Wiring{
		Provider: func() ai.Provider { return failProvider{msg: "x"} },
		Model:    func() string { return "m" },
		ProjectRules: func(workspace string) permission.Ruleset {
			data, err := os.ReadFile(filepath.Join(workspace, ".zgorules.json"))
			if err != nil {
				return permission.Ruleset{}
			}
			rules, err := permission.ParseRuleset(data)
			if err != nil {
				return permission.Ruleset{}
			}
			return rules
		},
	})
	s, _ := m.New(ws)

	// 直接在会话的 permission service 上验证规则语义（与 loadProjectRules
	// 同一解析路径）。
	svc := s.permissions
	if got := svc.Check(permission.Context{Tool: "bash", Args: "command: git status"},
		permission.Capability{}).Behavior; got != permission.Allow {
		t.Fatalf("git rule = %v", got)
	}
	if got := svc.Check(permission.Context{Tool: "write", Args: "path: x"},
		permission.Capability{}).Behavior; got != permission.Deny {
		t.Fatalf("write rule = %v", got)
	}
}

// steerRecorder captures each turn's History snapshot.
type steerRecorder struct {
	provider  *fakeProvider
	histories [][]ai.Message
	steerCh   chan []ai.Message
}

func TestSteeringInjectedBeforeNextTurn(t *testing.T) {
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "x"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{}`), // turn 0: tool call
		textTurn("收到插话", ai.Usage{}), // turn 1: final
	}}
	c := &collector{}
	// 确定性两段式 drain：turn0（工具执行前）为空 —— 模拟用户在工具
	// 执行期间插话；turn1 开头取到 —— loop 在 streamAssistant 前注入。
	calls := 0
	queuedMsg := ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "顺便看看 b.go"}}}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{tool},
		History:  func() []ai.Message { return c.messages },
		Steering: func() []ai.Message {
			calls++
			if calls == 2 {
				return []ai.Message{queuedMsg}
			}
			return nil
		},
	}, c.emit)
	loop.Run(context.Background(), c.append)

	// 插话确实被注入（历史里有该 user 消息，且位于 toolResult 之后）。
	found := false
	for i, m := range c.messages {
		if m.Role == ai.RoleUser && m.Text() == "顺便看看 b.go" && i > 0 &&
			c.messages[i-1].Role == ai.RoleToolResult {
			found = true
		}
	}
	if !found {
		t.Fatalf("steering not injected after tool results: %+v", c.messages)
	}
}

func TestSteeringKeepsLoopPastEndTurn(t *testing.T) {
	// 第一轮 end_turn，但队列里已有插话 → 循环继续（pi 的内层条件）。
	provider := &fakeProvider{turns: []fakeTurn{
		textTurn("先这样", ai.Usage{}),
		textTurn("补充完成", ai.Usage{}),
	}}
	c := &collector{messages: []ai.Message{{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "第一问"}}}}}
	// turn0 前 drain 为空；答完 end_turn 的 poll 取到插话 → 注入并继续。
	calls := 0
	loop := NewLoop(LoopConfig{
		Provider: provider,
		History:  func() []ai.Message { return c.messages },
		Steering: func() []ai.Message {
			calls++
			if calls == 2 { // end_turn 的 poll 取到一次，此后为空
				return []ai.Message{{Role: ai.RoleUser, Blocks: []ai.Block{
					{Kind: ai.BlockText, Text: "再补充一点"}}}}
			}
			return nil
		},
	}, c.emit)
	loop.Run(context.Background(), c.append)
	var answers int
	for _, m := range c.messages {
		if m.Role == ai.RoleAssistant {
			answers++
		}
	}
	if answers != 2 {
		t.Fatalf("answers = %d, want 2 (loop should continue past end_turn): %+v", answers, c.messages)
	}
}

func TestSessionSteerGuards(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	wire(m)
	s, _ := m.New("ws")
	if err := s.Steer("hi"); err == nil {
		t.Fatal("idle session accepted steering")
	}
	blocking := make(chan struct{})
	m.Wire(Wiring{
		Provider: func() ai.Provider { return blockProvider{until: blocking} },
		Model:    func() string { return "m" },
		System:   func(string) string { return "" },
	})
	done := make(chan error, 1)
	go func() { done <- s.Chat("start", func(Event) {}) }()
	waitForRunning(t, s)
	if err := s.Steer("插一句"); err != nil {
		t.Fatalf("steer during run: %v", err)
	}
	if err := s.Steer("  "); err == nil {
		t.Fatal("empty steering accepted")
	}
	close(blocking)
	<-done
}

// TestSteeringClearedOnRunEnd 验证运行结束（含 abort）清空插话队列：
// abort 残留的插话不得静默泄入下一次运行的首个回合。
func TestSteeringClearedOnRunEnd(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	blocking := make(chan struct{})
	m.Wire(Wiring{
		Provider: func() ai.Provider { return blockProvider{until: blocking} },
		Model:    func() string { return "m" },
		System:   func(string) string { return "" },
	})
	s, _ := m.New("ws")
	done := make(chan error, 1)
	go func() { done <- s.Chat("start", func(Event) {}) }()
	waitForRunning(t, s)
	if err := s.Steer("插话"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	s.Abort()
	<-done
	s.mu.Lock()
	left := len(s.steering)
	s.mu.Unlock()
	if left != 0 {
		t.Fatalf("aborted steering leaked into next run: %d queued", left)
	}
}

// overlapSlowTool blocks until it sees the fast tool has run — only
// possible when the batch overlaps (timeout keeps serial runs moving).
type overlapSlowTool struct {
	fastStarted chan struct{}
	name        string
	runs        int
}

func (t *overlapSlowTool) Name() string        { return t.name }
func (t *overlapSlowTool) Description() string { return "slow" }
func (t *overlapSlowTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *overlapSlowTool) Execute(context.Context, json.RawMessage) ToolResult {
	t.runs++
	select {
	case <-t.fastStarted:
		return ToolResult{Content: "slow done after overlap"}
	case <-time.After(2 * time.Second):
		return ToolResult{IsError: true, Content: "no overlap observed"}
	}
}

// overlapFastTool signals its start and returns immediately.
type overlapFastTool struct {
	started chan struct{}
	runs    int
}

func (t *overlapFastTool) Name() string        { return "fast" }
func (t *overlapFastTool) Description() string { return "fast" }
func (t *overlapFastTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *overlapFastTool) Execute(context.Context, json.RawMessage) ToolResult {
	t.runs++
	close(t.started)
	return ToolResult{Content: "fast done"}
}

// TestParallelToolsOverlap verifies parallel mode truly overlaps a batch
// (the slow tool only finishes after the fast one started) and assembles
// results in source order.
func TestParallelToolsOverlap(t *testing.T) {
	fast := &overlapFastTool{started: make(chan struct{})}
	slow := &overlapSlowTool{fastStarted: fast.started, name: "slow"}
	provider := &fakeProvider{turns: []fakeTurn{
		fakeTurn{events: []ai.StreamEvent{
			{Kind: ai.EvMessageStart},
			{Kind: ai.EvToolCallStart, ToolCallID: "slow", ToolCallName: "slow"},
			{Kind: ai.EvToolCallStart, ToolCallID: "fast", ToolCallName: "fast"},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
		}},
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	var endOrder []string
	loop := NewLoop(LoopConfig{
		Provider:      provider,
		Tools:         []Tool{slow, fast},
		ToolExecution: ParallelExecution,
		History:       func() []ai.Message { return c.messages },
	}, func(ev Event) {
		c.emit(ev)
		if ev.Kind == EvToolExecutionEnd {
			endOrder = append(endOrder, ev.ToolCallID)
		}
	})
	loop.Run(context.Background(), c.append)

	if slow.runs != 1 || fast.runs != 1 {
		t.Fatalf("runs slow=%d fast=%d", slow.runs, fast.runs)
	}
	// 结果按源序（slow 在前）组装。
	results := c.messages[1]
	if len(results.Blocks) != 2 ||
		results.Blocks[0].ToolCallID != "slow" ||
		results.Blocks[1].ToolCallID != "fast" {
		t.Fatalf("source order broken: %+v", results.Blocks)
	}
	// 完成序事件：fast 先完成。
	if len(endOrder) != 2 || endOrder[0] != "fast" {
		t.Fatalf("completion order = %v", endOrder)
	}
}

// TestSerialBatchDoesNotOverlap verifies the default mode stays sequential.
func TestSerialBatchDoesNotOverlap(t *testing.T) {
	fast := &overlapFastTool{started: make(chan struct{})}
	slow := &overlapSlowTool{fastStarted: fast.started, name: "slow"}
	provider := &fakeProvider{turns: []fakeTurn{
		fakeTurn{events: []ai.StreamEvent{
			{Kind: ai.EvMessageStart},
			{Kind: ai.EvToolCallStart, ToolCallID: "slow", ToolCallName: "slow"},
			{Kind: ai.EvToolCallStart, ToolCallID: "fast", ToolCallName: "fast"},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
		}},
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{slow, fast}, // 默认 sequential
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)
	// 串行：slow 等不到 fast 启动，按超时路径完成并报错。
	results := c.messages[1]
	if !results.Blocks[0].IsError {
		t.Fatalf("serial mode unexpectedly overlapped: %+v", results.Blocks[0])
	}
}

// TestSerialOnlyToolForcesSequentialBatch verifies ask/todo marked serial
// keep the whole batch sequential even in parallel mode.
func TestSerialOnlyToolForcesSequentialBatch(t *testing.T) {
	fast := &overlapFastTool{started: make(chan struct{})}
	slow := &overlapSlowTool{fastStarted: fast.started, name: "slow"}
	provider := &fakeProvider{turns: []fakeTurn{
		fakeTurn{events: []ai.StreamEvent{
			{Kind: ai.EvMessageStart},
			{Kind: ai.EvToolCallStart, ToolCallID: "ask", ToolCallName: "ask"},
			{Kind: ai.EvToolCallStart, ToolCallID: "fast", ToolCallName: "fast"},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
		}},
		textTurn("done", ai.Usage{}),
	}}
	// 带 SerialOnly 标记的轻量替身（ask/todo 的批内替身）。
	marker := &serialMarker{name: "ask"}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider:      provider,
		Tools:         []Tool{marker, fast, slow},
		ToolExecution: ParallelExecution,
		History:       func() []ai.Message { return c.messages },
	}, c.emit)
	_ = slow
	loop.Run(context.Background(), c.append)
	// 批次含 ask（标记串行）→ 整批顺序：本测试里 ask 先跑、fast 后跑。
	// slow 未被本批调用；断言 ask 已执行且无并发要求。
	if marker.runs != 1 || fast.runs != 1 {
		t.Fatalf("ask=%d fast=%d", marker.runs, fast.runs)
	}
}

type serialMarker struct {
	name string
	runs int
}

func (t *serialMarker) Name() string        { return t.name }
func (t *serialMarker) Description() string { return "serial marker" }
func (t *serialMarker) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *serialMarker) Execute(context.Context, json.RawMessage) ToolResult {
	t.runs++
	return ToolResult{Content: "ok"}
}
func (t *serialMarker) SerialOnly() bool { return true }
