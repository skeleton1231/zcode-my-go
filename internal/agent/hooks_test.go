package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestBeforeHookBlocks verifies a blocking before-hook never reaches the
// tool and its reason reaches the model.
func TestBeforeHookBlocks(t *testing.T) {
	tool := &fakeTool{name: "write", agent: ToolResult{Content: "wrote"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "write", `{}`),
		textTurn("adapted", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{tool},
		Hooks: Hooks{BeforeToolCall: func(BeforeToolCallContext) BeforeToolCallResult {
			return BeforeToolCallResult{Block: true, Reason: "禁止写文件"}
		}},
		History: func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)

	if tool.runs != 0 {
		t.Fatal("blocked call executed")
	}
	block := c.messages[1].Blocks[0]
	if !block.IsError || !strings.Contains(block.Text, "hook blocked") ||
		!strings.Contains(block.Text, "禁止写文件") {
		t.Fatalf("blocked result = %+v", block)
	}
}

// TestBeforeHookTerminate verifies the terminate hint ends the run after
// the batch (results already recorded).
func TestBeforeHookTerminate(t *testing.T) {
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "x"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{}`),
		textTurn("never", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{tool},
		Hooks: Hooks{BeforeToolCall: func(BeforeToolCallContext) BeforeToolCallResult {
			return BeforeToolCallResult{Terminate: true, Reason: "足够了"}
		}},
		History: func() []ai.Message { return c.messages },
	}, c.emit)
	stop := loop.Run(context.Background(), c.append)
	if stop != ai.StopEndTurn {
		t.Fatalf("stop = %v", stop)
	}
	if tool.runs != 1 {
		t.Fatalf("terminate blocked execution: runs=%d", tool.runs)
	}
	end := c.events[len(c.events)-1]
	if !strings.Contains(end.ErrorMessage, "足够了") {
		t.Fatalf("agent_end = %+v", end)
	}
}

// TestAfterHookRewrites verifies an after-hook's rewrite lands.
func TestAfterHookRewrites(t *testing.T) {
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "raw"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{}`),
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Tools:    []Tool{tool},
		Hooks: Hooks{AfterToolCall: func(c AfterToolCallContext) AfterToolCallResult {
			if c.ToolCall.Name != "read" {
				return AfterToolCallResult{}
			}
			return AfterToolCallResult{Changed: true, Content: c.Result.Content + "（已审阅）"}
		}},
		History: func() []ai.Message { return c.messages },
	}, c.emit)
	loop.Run(context.Background(), c.append)
	if got := c.messages[1].Blocks[0].Text; got != "raw（已审阅）" {
		t.Fatalf("rewritten = %q", got)
	}
}

// TestHookPreHelper / TestHookPostHelper 不是真测试：pre/post 两个 hook
// 命令宿主。阶段由 -test.run 目标区分（PowerShell 的 --% 与 stdin 转发
// 组合会丢 stdin，不能靠 payload 判定），策略由 env 决定。
func TestHookPreHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "block" {
		return // 普通 go test 直跑：不是 hook 调用
	}
	os.Stderr.WriteString("该工具被策略禁止")
	os.Exit(1)
}

func TestHookPostHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "feedback" {
		return // 普通 go test 直跑：不是 hook 调用
	}
	os.Stderr.WriteString("结果需要复核")
	os.Exit(2)
}

// hookCommand 生成指向某个 helper 的命令串（cmd /c 原样传递参数与
// 退出码）。
func hookCommand(helper string) string {
	return os.Args[0] + " -test.run=" + helper
}

// TestCommandHooks end-to-end: settings-shaped command hooks block and
// feed back through real processes.
func TestCommandHooks(t *testing.T) {
	pre := hookCommand("TestHookPreHelper")
	post := hookCommand("TestHookPostHelper")
	env := func(name, value string) {
		os.Setenv(name, value)
	}
	env("ZGO_HOOK_MODE", "block")
	t.Cleanup(func() { os.Unsetenv("ZGO_HOOK_MODE") })

	hooks := HooksFromCommands(CommandHooks{
		PreToolUse:  []CommandHook{{Command: pre}},
		PostToolUse: []CommandHook{{Command: post}},
	})

	// block 阶段：pre 收到无 result 的 payload → exit 1 + stderr 原因。
	tool := &fakeTool{name: "write", agent: ToolResult{Content: "wrote"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "write", `{}`),
		textTurn("ok", ai.Usage{}),
	}}
	c := &collector{}
	NewLoop(LoopConfig{
		Provider: provider, Tools: []Tool{tool}, Hooks: hooks,
		History: func() []ai.Message { return c.messages },
	}, c.emit).Run(context.Background(), c.append)
	if tool.runs != 0 {
		t.Fatal("command hook did not block")
	}
	if got := c.messages[1].Blocks[0].Text; !strings.Contains(got, "该工具被策略禁止") {
		t.Fatalf("block reason = %q", got)
	}

	// feedback 阶段：pre 走 exit 0 放行，post（payload 含 result）exit 2 → 反馈。
	env("ZGO_HOOK_MODE", "feedback")
	tool2 := &fakeTool{name: "read", agent: ToolResult{Content: "raw"}}
	provider2 := &fakeProvider{turns: []fakeTurn{
		toolTurn("c2", "read", `{}`),
		textTurn("ok", ai.Usage{}),
	}}
	c2 := &collector{}
	hooks2 := HooksFromCommands(CommandHooks{
		PostToolUse: []CommandHook{{Command: post}},
	})
	NewLoop(LoopConfig{
		Provider: provider2, Tools: []Tool{tool2}, Hooks: hooks2,
		History: func() []ai.Message { return c2.messages },
	}, c2.emit).Run(context.Background(), c2.append)
	if got := c2.messages[1].Blocks[0].Text; !strings.Contains(got, "[hook 反馈] 结果需要复核") {
		t.Fatalf("feedback = %q", got)
	}
}

// TestHookPostDirect 单元级验证 after 命令钩子的退出码/反馈路径。
func TestHookPostDirect(t *testing.T) {
	os.Setenv("ZGO_HOOK_MODE", "feedback")
	defer os.Unsetenv("ZGO_HOOK_MODE")
	after := afterFromCommands([]CommandHook{{Command: hookCommand("TestHookPostHelper")}})
	if after == nil {
		t.Fatal("nil hook")
	}
	res := after(AfterToolCallContext{
		ToolCall: ai.Block{Kind: ai.BlockToolCall, Name: "read"},
		Result:   ToolResult{Content: "raw"},
	})
	if !res.Changed || !strings.Contains(res.Content, "结果需要复核") {
		t.Fatalf("direct after = %+v", res)
	}
}

// TestHookSessionHelper：会话生命周期钩子的观察者宿主——stdin 原样追加
// 到 ZGO_HOOK_FILE（mode=session）。
func TestHookSessionHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "session" {
		return // 普通 go test 直跑：不是钩子调用
	}
	data, _ := io.ReadAll(os.Stdin)
	f, err := os.OpenFile(os.Getenv("ZGO_HOOK_FILE"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(0)
	}
	f.Write(append(data, '\n'))
	f.Close()
	os.Exit(0)
}

// TestHookCompactBlockHelper：preCompact 的阻止者宿主——stderr 给原因并
// 退出 1。
func TestHookCompactBlockHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "block-compact" {
		return
	}
	os.Stderr.WriteString("预算紧张，先别压")
	os.Exit(1)
}

// TestSessionObserverHooks：sessionStart/sessionEnd 观察者语义——stdin
// 收到 payload JSON，不返回任何阻断。
func TestSessionObserverHooks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hooks.log")
	t.Setenv("ZGO_HOOK_FILE", file)
	t.Setenv("ZGO_HOOK_MODE", "session")
	cmd := hookCommand("TestHookSessionHelper")

	SessionStartHooks([]CommandHook{{Command: cmd}},
		map[string]any{"sessionId": "s1", "workspace": "/w"})
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), `"sessionId":"s1"`) {
		t.Fatalf("sessionStart payload not delivered: %v %q", err, data)
	}
	// 再跑 sessionEnd：同宿主可复用（追加了第二行）。
	SessionEndHooks([]CommandHook{{Command: cmd}},
		map[string]any{"sessionId": "s1", "reason": "close"})
	data, _ = os.ReadFile(file)
	if c := strings.Count(string(data), "\n"); c != 2 {
		t.Fatalf("sessionEnd not fired: %d lines", c)
	}
	// 空钩子表是 no-op，不panic。
	SessionStartHooks(nil, nil)
}

// TestPreCompactCommandHooksBlocks：非零退出阻止压缩，stderr 是原因。
func TestPreCompactCommandHooksBlocks(t *testing.T) {
	t.Setenv("ZGO_HOOK_MODE", "block-compact")
	blocked, reason := PreCompactHooks(
		[]CommandHook{{Command: hookCommand("TestHookCompactBlockHelper")}},
		map[string]any{"messages": 10})
	if !blocked || reason != "预算紧张，先别压" {
		t.Fatalf("blocked=%v reason=%q", blocked, reason)
	}
	// 全零退出放行。
	t.Setenv("ZGO_HOOK_MODE", "session")
	blocked, reason = PreCompactHooks(
		[]CommandHook{{Command: hookCommand("TestHookSessionHelper")}}, nil)
	if blocked || reason != "" {
		t.Fatalf("zero exit blocked: %v %q", blocked, reason)
	}
}
