package e2e

// 后台子代理（spec 规则 49）的全栈 e2e：agent 启动即回执、父会话继续
// 回合、agent_output 从真实注册表收割子代理结论（子代理经 Script 的
// 嵌套 provider 请求真实跑完）。

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestE2EBackgroundAgentSpawnAndCollect(t *testing.T) {
	// 回合编排（子任务吃一回合）：①agent 启动调用 ②子代理响应 ③文本
	// 收尾第一次 Ask ④agent_output 调用 ⑤文本收尾。
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "agent", Args: `{"prompt":"调查模块结构"}`}}},
		Turn{Text: "模块共三层"},
		Turn{Text: "继续别的活"},
		Turn{ToolCalls: []ToolCall{{Name: "agent_output", Args: `{"id":"ag-1"}`}}},
		Turn{Text: "收到结论"},
	)

	events := h.Ask("后台查一下")
	spawned := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "agent" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "ag-1 已启动") {
				spawned = true
			}
		}
	}
	if !spawned {
		t.Fatal("spawn receipt never surfaced")
	}

	events = h.Ask("收割")
	collected := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "agent_output" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "模块共三层") {
				collected = true
			}
		}
	}
	if !collected {
		t.Fatal("subagent answer never collected")
	}
}

// TestE2ETaskSubagentReadsRemote ssh:// 会话的子代理经网线读文件——
// 此前嵌套循环用字符串重建本地工具面，远程会话的 task/dispatch 读的
// 是本地磁盘（spec 规则 49 顺带修复的回归面）。
func TestE2ETaskSubagentReadsRemote(t *testing.T) {
	// 回合编排：①task 调用 ②子代理的 read 调用 ③子代理结论文本 ④收尾。
	h := NewRemote(t,
		Turn{ToolCalls: []ToolCall{{Name: "task", Args: `{"prompt":"读 remote-only.txt 汇报内容"}`}}},
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"remote-only.txt"}`}}},
		Turn{Text: "内容已读"},
		Turn{Text: "完成"},
	)
	if err := h.Probe.WriteFile("remote-only.txt", []byte("仅远端存在的答案"), 0o644); err != nil {
		t.Fatalf("plant remote file: %v", err)
	}

	h.Ask("查一下")
	// 子代理的 read 事件不透传父流（嵌套循环各自的事件面）——断言改
	// 在记录的 provider 请求上：read 之后的子任务请求应携带其结果
	// （若读了本地磁盘，结果为文件不存在的错误，内容不会出现）。
	// 请求序：[0] 父 task 调用 [1] 子代理 read 调用 [2] 子代理收到
	// read 结果后作答 [3] 父收尾。
	found := false
	for _, m := range h.Script.Requests[2].Messages {
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockToolResult && strings.Contains(b.Text, "仅远端存在的答案") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("subagent's read result (remote content) never reached its context")
	}
}
