package agent

// Stop / Notification 命令钩子（spec 规则 68，issue #209）：exit-2 阻止
// 收尾并注入续跑消息；notification 观察者递送 payload。命令经测试二进
// 制自调（跨平台，同 hooks_test 的 helper 模式）。

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestHookStopHelper(t *testing.T) {
	switch os.Getenv("ZGO_HOOK_MODE") {
	case "stop-block":
		io.Copy(io.Discard, os.Stdin) // 吃掉 payload（块场景不读内容）
		os.Stderr.WriteString("测试还没跑完")
		os.Exit(2)
	case "stop-payload": // payload 落文件（递达证明），输出留空 → 默认原因
		data, _ := io.ReadAll(os.Stdin)
		os.WriteFile(os.Getenv("ZGO_HOOK_FILE"), data, 0o644)
		os.Exit(2)
	case "stop-observe":
		os.Exit(1) // 非零非 2：记录不阻断
	}
}

func TestHookNotifyHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "notify" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	f, err := os.OpenFile(os.Getenv("ZGO_HOOK_FILE"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(3)
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}

// TestStopCommandHooks：exit 2 阻止收尾（输出为原因）；非 2 非零放行；
// 空 exit-2 输出给默认原因。
func TestStopCommandHooks(t *testing.T) {
	t.Setenv("ZGO_HOOK_MODE", "stop-block")
	stop := stopFromCommands([]CommandHook{{Command: hookCommand("TestHookStopHelper")}})
	r := stop(StopContext{SessionID: "s1", Workspace: "/w", LastText: "完了"})
	if !r.Blocked || r.Reason != "测试还没跑完" {
		t.Fatalf("exit-2 semantics: %+v", r)
	}

	t.Setenv("ZGO_HOOK_MODE", "stop-observe")
	r = stop(StopContext{SessionID: "s1"})
	if r.Blocked {
		t.Fatalf("exit-1 should not block: %+v", r)
	}

	t.Setenv("ZGO_HOOK_MODE", "stop-payload") // payload 落文件、输出空 → 默认原因
	file := filepath.Join(t.TempDir(), "stop-payload.json")
	t.Setenv("ZGO_HOOK_FILE", file)
	r = stop(StopContext{SessionID: "sess-9", Workspace: "/ws/x", LastText: "最后回答"})
	if !r.Blocked || r.Reason != "stop hook requested continuation" {
		t.Fatalf("empty-output block default: %+v", r)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), `"sessionId":"sess-9"`) ||
		!strings.Contains(string(data), "最后回答") {
		t.Fatalf("stop payload not delivered: %v %s", err, data)
	}
}

// TestStopCommandHooksPayload：payload 字段（event/sessionId/workspace/
// lastText）组装契约（spec 规则 68）。
func TestStopCommandHooksPayload(t *testing.T) {
	got := string(stopPayloadJSON(StopContext{SessionID: "sess-9", Workspace: "/ws/x", LastText: "最后回答"}))
	for _, want := range []string{`"event":"stop"`, `"sessionId":"sess-9"`, `"/ws/x"`, "最后回答"} {
		if !strings.Contains(got, want) {
			t.Fatalf("payload missing %s: %s", want, got)
		}
	}
}

// TestStopHookForcesContinuation：loop 级——自然收尾被 stop 钩子阻止，
// 续跑消息进转录、第二回合执行，随后放行收尾。
func TestStopHookForcesContinuation(t *testing.T) {
	provider := &fakeProvider{turns: []fakeTurn{
		textTurn("第一轮回答", ai.Usage{}),
		textTurn("第二轮回答", ai.Usage{}),
	}}
	c := &collector{}
	blocked := true // 第一次询问阻止，之后放行
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Hooks: Hooks{Stop: func(StopContext) StopResult {
			r := StopResult{Blocked: blocked, Reason: "还有测试没跑"}
			blocked = false
			return r
		}},
		History: func() []ai.Message { return c.messages },
	}, c.emit)
	stop := loop.Run(context.Background(), c.append)

	if stop != ai.StopEndTurn {
		t.Fatalf("final stop: %v", stop)
	}
	joined := ""
	for _, m := range c.messages {
		joined += m.Text() + "\n"
	}
	if !strings.Contains(joined, "第一轮回答") || !strings.Contains(joined, "第二轮回答") {
		t.Fatalf("second turn never ran: %q", joined)
	}
	if !strings.Contains(joined, "[stop 钩子]") || !strings.Contains(joined, "还有测试没跑") {
		t.Fatalf("continuation message missing: %q", joined)
	}
}

// TestStopHookCappedByTurns：钩子一直阻止时 MaxTurns 兜底（防死循环）。
func TestStopHookCappedByTurns(t *testing.T) {
	provider := &fakeProvider{turns: []fakeTurn{
		textTurn("又", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		MaxTurns: 2,
		Hooks:    Hooks{Stop: func(StopContext) StopResult { return StopResult{Blocked: true, Reason: "不许停"} }},
		History:  func() []ai.Message { return c.messages },
	}, c.emit)
	stop := loop.Run(context.Background(), c.append)
	if stop != ai.StopEndTurn {
		t.Fatalf("stop: %v", stop)
	}
}

// TestNotificationObserverHooks：payload 递达通知命令（观察者）。
func TestNotificationObserverHooks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notify.log")
	t.Setenv("ZGO_HOOK_FILE", file)
	t.Setenv("ZGO_HOOK_MODE", "notify")
	NotificationHooks([]CommandHook{{Command: hookCommand("TestHookNotifyHelper")}},
		map[string]any{"event": "notification", "sessionId": "s1",
			"workspace": "/w", "tool": "write", "reason": "需要确认"})
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), `"tool":"write"`) ||
		!strings.Contains(string(data), "需要确认") {
		t.Fatalf("notification payload not delivered: %v %q", err, data)
	}
	// 空钩子表 no-op。
	NotificationHooks(nil, nil)
}
