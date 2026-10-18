package main

// print 模式的结构化输出（规则 23，Claude Code --output-format json 的
// 对应物）：每个 agent 事件一行紧凑 JSON 到 stdout，供脚本与管道消费
// （cron 任务、编辑器集成）。字段名稳定；错误与用量提示仍走 stderr。

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// jsonEvent 是所有事件形态的并集（omitempty 折叠出每类事件的实际字
// 段）；type 取值：text、tool_use、tool_result、compaction、retry、
// message_end、result。
type jsonEvent struct {
	Type string `json:"type"`

	Text   string `json:"text,omitempty"`   // text / compaction / retry
	Tool   string `json:"tool,omitempty"`   // tool_use / tool_result
	Args   any    `json:"args,omitempty"`   // tool_use：原始 JSON 内嵌
	Result string `json:"result,omitempty"` // tool_result
	IsErr  bool   `json:"is_error,omitempty"`
	Diff   string `json:"diff,omitempty"`

	StopReason string `json:"stop_reason,omitempty"` // message_end / result
	Error      string `json:"error,omitempty"`
}

// renderJSON prints one JSON line per event (json mode of render).
func renderJSON(ev agent.Event) {
	var e jsonEvent
	switch ev.Kind {
	case agent.EvTextDelta:
		e = jsonEvent{Type: "text", Text: ev.Text}
	case agent.EvToolExecutionStart:
		e = jsonEvent{Type: "tool_use", Tool: ev.ToolName, Args: rawJSON(ev.Args)}
	case agent.EvToolExecutionEnd:
		e = jsonEvent{Type: "tool_result", Tool: ev.ToolName,
			Result: ev.ResultContent, IsErr: ev.IsError, Diff: ev.Diff}
	case agent.EvCompaction:
		e = jsonEvent{Type: "compaction", Text: ev.Text}
	case agent.EvTurnRetry:
		e = jsonEvent{Type: "retry", Text: ev.Text}
	case agent.EvMessageEnd:
		e = jsonEvent{Type: "message_end", StopReason: string(ev.StopReason),
			Error: ev.ErrorMessage}
	case agent.EvAgentEnd:
		e = jsonEvent{Type: "result", StopReason: string(ev.StopReason),
			Error: ev.ErrorMessage}
	default:
		return // 流式细节（message_start/tool call 聚合等）不外发
	}
	line, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[json] 序列化失败：%v\n", err)
		return
	}
	osStdout.Write(append(line, '\n'))
}

// rawJSON embeds the tool args as raw JSON when valid, degrading to the
// string otherwise（模型产出畸形 JSON 时仍可见原样）.
func rawJSON(s string) any {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return s
}

// osStdout 是输出缝（测试捕获用），生产即 os.Stdout。
var osStdout io.Writer = os.Stdout
