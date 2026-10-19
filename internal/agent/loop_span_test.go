package agent

// 遥测 span 集成（spec 规则 91）：脚本回合跑真 loop，断言 turn/
// model/tool 三类 span 的父子链与属性。compact span 由 service 侧
// e2e 覆盖（compact 需要完整 session 语境）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/telemetry"
)

func loopSpans(t *testing.T, dataDir string) []map[string]any {
	t.Helper()
	path := filepath.Join(dataDir, "traces", "trace-"+time.Now().Format("20060102")+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse span: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// TestLoopSpanChain：工具回合产生 agent.turn（根）→ model.call×2（工具
// 回合 + 收尾回合）+ tool.call×1，全部挂在 turn 之下。
func TestLoopSpanChain(t *testing.T) {
	dir := t.TempDir()
	tr := telemetry.New(true, "file", "", dir)
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "file body"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{"path":"a.txt"}`),
		textTurn("done", ai.Usage{InputTokens: 7, OutputTokens: 3}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider:  provider,
		Model:     "fake-model",
		Tools:     []Tool{tool},
		History:   func() []ai.Message { return c.messages },
		Telemetry: tr,
		SessionID: "sess-1",
		Workspace: "ws-1",
	}, c.emit)
	loop.Run(context.Background(), c.append)

	spans := loopSpans(t, dir)
	byName := map[string][]map[string]any{}
	for _, s := range spans {
		byName[s["name"].(string)] = append(byName[s["name"].(string)], s)
	}
	turns := byName["agent.turn"]
	if len(turns) != 1 {
		t.Fatalf("agent.turn spans = %d", len(turns))
	}
	turn := turns[0]
	if turn["parentSpanId"] != nil && turn["parentSpanId"] != "" {
		t.Fatalf("turn span has parent %v", turn["parentSpanId"])
	}
	attrs := turn["attrs"].(map[string]any)
	if attrs["session.id"] != "sess-1" || attrs["workspace"] != "ws-1" ||
		attrs["model"] != "fake-model" || attrs["stop.reason"] != string(ai.StopEndTurn) {
		t.Fatalf("turn attrs = %v", attrs)
	}
	if turn["status"] != telemetry.StatusOK {
		t.Fatalf("turn status = %v", turn["status"])
	}

	models := byName["model.call"]
	if len(models) != 2 {
		t.Fatalf("model.call spans = %d", len(models))
	}
	for _, m := range models {
		if m["parentSpanId"] != turn["spanId"] {
			t.Fatalf("model span parent = %v", m["parentSpanId"])
		}
	}
	last := models[len(models)-1]
	mAttrs := last["attrs"].(map[string]any)
	if mAttrs["input_tokens"] != "7" || mAttrs["output_tokens"] != "3" {
		t.Fatalf("model usage attrs = %v", mAttrs)
	}

	tools := byName["tool.call"]
	if len(tools) != 1 {
		t.Fatalf("tool.call spans = %d", len(tools))
	}
	tAttrs := tools[0]["attrs"].(map[string]any)
	if tAttrs["tool"] != "read" || tAttrs["is_error"] != "false" {
		t.Fatalf("tool attrs = %v", tAttrs)
	}
	if tools[0]["parentSpanId"] != turn["spanId"] {
		t.Fatal("tool span not under turn")
	}
	// 参数永不进遥测：a.txt 只该出现在转录，不出现在 span 行。
	raw, _ := os.ReadFile(filepath.Join(dir, "traces",
		"trace-"+time.Now().Format("20060102")+".jsonl"))
	if strings.Contains(string(raw), "a.txt") {
		t.Fatal("tool arguments leaked into telemetry")
	}
}

// TestLoopNilTelemetryUnchanged：nil tracer（默认）下回合行为与事件序
// 列完全不变——零开销路径不改变语义。
func TestLoopNilTelemetryUnchanged(t *testing.T) {
	tool := &fakeTool{name: "read", agent: ToolResult{Content: "body"}}
	provider := &fakeProvider{turns: []fakeTurn{
		toolTurn("c1", "read", `{}`),
		textTurn("done", ai.Usage{}),
	}}
	c := &collector{}
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Model:    "fake-model",
		Tools:    []Tool{tool},
		History:  func() []ai.Message { return c.messages },
		// Telemetry 不设——nil 是默认路径。
	}, c.emit)
	stop := loop.Run(context.Background(), c.append)
	if stop != ai.StopEndTurn {
		t.Fatalf("stop = %v", stop)
	}
	want := "agent_start turn_start message_start tool_call_start tool_call_delta message_end tool_execution_start tool_execution_end turn_end turn_start message_start text_delta message_end turn_end agent_end"
	if got := c.kinds(); got != want {
		t.Fatalf("events = %q", got)
	}
}
