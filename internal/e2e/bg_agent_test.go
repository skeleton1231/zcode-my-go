package e2e

// 后台子代理（spec 规则 49）的全栈 e2e：agent 启动即回执、父会话继续
// 回合、agent_output 从真实注册表收割子代理结论。
//
// 编排不按 Script 请求序号（后台子代理与父续跑的请求到达顺序是真实
// 竞态——CI linux 快 runner 上曾吃错回合 flaky），改按请求内容路由：
// 含 spawn 提词的请求属子代理，回固定结论；其余属父回合，回
// agent_output 调用后收尾。agent_output 自带 2s 有界等待，收割确定性
// 由「子代理流已开闸 + 有界等待」共同保证。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// routedProvider 按首条用户消息内容路由回合（后台子代理 e2e 专用）。
type routedProvider struct {
	mu     sync.Mutex
	parent int // 父回合序号
}

func (p *routedProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 8)
	first := ""
	for _, m := range req.Messages {
		if m.Role == ai.RoleUser {
			first = m.Text()
			break
		}
	}
	isSub := strings.Contains(first, "调查模块结构")
	go func() {
		defer close(out)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		if isSub {
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "模块共三层"}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
			return
		}
		p.mu.Lock()
		p.parent++
		n := p.parent
		p.mu.Unlock()
		switch {
		case n%2 == 0: // 偶数序：收割（Ask1 顺势一次 + Ask2 的显式收割）
			out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "c" + fmt.Sprint(n), ToolCallName: "agent_output"}
			out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "c" + fmt.Sprint(n),
				ToolCallArgs: `{"id":"ag-1"}`}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
		case n == 1: // 第一回合：agent 启动
			out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "c1", ToolCallName: "agent"}
			out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "c1",
				ToolCallArgs: `{"prompt":"调查模块结构"}`}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
		default: // 文本收尾（回合自然结束）
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "继续别的活"}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		}
	}()
	return out
}

func TestE2EBackgroundAgentSpawnAndCollect(t *testing.T) {
	h := New(t) // 不用脚本回合：内容由 routedProvider 路由
	rp := &routedProvider{}
	h.Svc.UseProviderForTesting(func() ai.Provider { return rp })
	t.Cleanup(func() { h.Svc.UseProviderForTesting(nil) })

	events := h.Ask("后台查一下")
	spawned := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "agent" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "已启动") {
				spawned = true
			}
		}
	}
	if !spawned {
		t.Fatalf("spawn receipt never surfaced: %+v", events)
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
		t.Fatalf("subagent answer never collected: %+v", events)
	}
	_ = fmt.Sprint()
}
