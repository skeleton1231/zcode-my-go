package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// captureJSON 把一批事件经 renderJSON 打到缓冲并逐行解析回来。
func captureJSON(t *testing.T, events []agent.Event) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	old := osStdout
	osStdout = &buf
	defer func() { osStdout = old }()
	for _, ev := range events {
		renderJSON(ev)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad json line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// TestRenderJSONEventShapes 验证各事件形态的字段与折叠（规则 23）：
// 文本、工具调用（args 内嵌）、工具结果（is_error/diff）、压缩、重试、
// 回合结束（stop_reason）；message_start 等流式细节不外发。
func TestRenderJSONEventShapes(t *testing.T) {
	events := []agent.Event{
		{Kind: agent.EvMessageStart},
		{Kind: agent.EvTextDelta, Text: "你好"},
		{Kind: agent.EvToolExecutionStart, ToolName: "write", Args: `{"path":"a.txt"}`},
		{Kind: agent.EvToolExecutionEnd, ToolName: "write", ResultContent: "ok", Diff: "+a"},
		{Kind: agent.EvToolExecutionEnd, ToolName: "bash", IsError: true, ResultContent: "boom"},
		{Kind: agent.EvCompaction, Text: "已压缩"},
		{Kind: agent.EvTurnRetry, Text: "第 2 次尝试"},
		{Kind: agent.EvMessageEnd, StopReason: "end_turn"},
		{Kind: agent.EvAgentEnd, StopReason: "end_turn"},
	}
	got := captureJSON(t, events)
	if len(got) != 8 { // message_start 等流式细节不外发
		t.Fatalf("lines = %d, want 8: %v", len(got), got)
	}
	want := []map[string]any{
		{"type": "text", "text": "你好"},
		{"type": "tool_use", "tool": "write", "args": map[string]any{"path": "a.txt"}},
		{"type": "tool_result", "tool": "write", "result": "ok", "diff": "+a"},
		{"type": "tool_result", "tool": "bash", "result": "boom", "is_error": true},
		{"type": "compaction", "text": "已压缩"},
		{"type": "retry", "text": "第 2 次尝试"},
		{"type": "message_end", "stop_reason": "end_turn"},
		{"type": "result", "stop_reason": "end_turn"},
	}
	for i, w := range want {
		for k, v := range w {
			g, ok := got[i][k]
			if !ok {
				t.Errorf("line %d missing %q: %v", i, k, got[i])
				continue
			}
			// map/任意值不可直接 ==（args 是内嵌对象会 panic）：比较序列化形式
			gj, _ := json.Marshal(g)
			vj, _ := json.Marshal(v)
			if string(gj) != string(vj) {
				t.Errorf("line %d %q = %v, want %v", i, k, g, v)
			}
		}
	}
}

// TestRenderJSONInvalidArgsDegrades 验证畸形 args 降级为字符串。
func TestRenderJSONInvalidArgsDegrades(t *testing.T) {
	got := captureJSON(t, []agent.Event{
		{Kind: agent.EvToolExecutionStart, ToolName: "bash", Args: "{not-json"},
	})
	if got[0]["args"] != "{not-json" {
		t.Fatalf("args = %v, want raw string", got[0]["args"])
	}
}
