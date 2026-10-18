package e2e

// 子代理续话（spec 规则 51）的全栈 e2e：agent 启动 → agent_send 带全
// 部历史续跑（真实嵌套循环收到既有转录与追问）→ agent_output 收割新
// 结论。父/子请求经内容路由（后台并发不依赖回合次序）。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// sendRoutedProvider 按内容路由：末条用户消息含「调查模块结构」是子代
// 理首跑、「追问」是续跑，其余是父回合（按序号循环派发工具调用）。
type sendRoutedProvider struct {
	mu     sync.Mutex
	parent int
}

func (p *sendRoutedProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	last := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == ai.RoleUser {
			last = req.Messages[i].Text()
			break
		}
	}
	isFirst := strings.Contains(last, "调查模块结构")
	isResume := strings.Contains(last, "追问细节")
	out := make(chan ai.StreamEvent, 8)
	go func() {
		defer close(out)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		switch {
		case isFirst:
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "首次结论"}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		case isResume:
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "续话结论"}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		default:
			p.mu.Lock()
			p.parent++
			n := p.parent
			p.mu.Unlock()
			id := fmt.Sprintf("c%d", n)
			// 奇数序派工具（spawn→send→output 轮次推进），偶数序文本
			// 收尾——每次 Ask 恰好一个工具回合，终态等待夹在 Ask 之间。
			if n%2 == 0 {
				out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "收到"}
				out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
				return
			}
			switch (n + 1) / 2 {
			case 1:
				out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: id, ToolCallName: "agent"}
				out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: id,
					ToolCallArgs: `{"prompt":"调查模块结构"}`}
			case 2:
				out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: id, ToolCallName: "agent_send"}
				out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: id,
					ToolCallArgs: `{"id":"ag-1","message":"追问细节"}`}
			default:
				out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: id, ToolCallName: "agent_output"}
				out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: id,
					ToolCallArgs: `{"id":"ag-1"}`}
			}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
		}
	}()
	return out
}

func TestE2EAgentSendResumesWithHistory(t *testing.T) {
	h := New(t)
	rp := &sendRoutedProvider{}
	h.Svc.UseProviderForTesting(func() ai.Provider { return rp })
	t.Cleanup(func() { h.Svc.UseProviderForTesting(nil) })

	h.Ask("后台查一下") // 父回合 1：spawn ag-1

	// 等首跑终态，再续话（回合归属由真实调度决定——终态先行）。
	if !waitFor(5e9, func() bool {
		for _, v := range h.Svc.AgentJobStates(h.SessionID) {
			if v.ID == "ag-1" && v.State == tools.AgentDone && v.Text == "首次结论" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("first run never finished")
	}

	h.Ask("继续问") // 父回合 2：agent_send 续跑

	if !waitFor(5e9, func() bool {
		for _, v := range h.Svc.AgentJobStates(h.SessionID) {
			if v.ID == "ag-1" && v.State == tools.AgentDone && v.Text == "续话结论" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("resumed run never finished")
	}

	events := h.Ask("收割") // 父回合 3：agent_output
	collected := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "agent_output" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "续话结论") {
				collected = true
			}
		}
	}
	if !collected {
		t.Fatal("resumed answer never collected")
	}
}
