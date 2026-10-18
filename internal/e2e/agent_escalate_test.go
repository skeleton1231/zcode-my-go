package e2e

// 子代理升级提问（spec 规则 63，ZCode escalate 的落地）的全栈 e2e：
// 后台代理的脚本回合调用 ask → 经会话提问通道升级（带代理 ID 前缀）
// → 答案回转录、代理据此作答完成。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// escalateRoutedProvider 按内容路由：代理请求的回合序驱动 ask → 收到
// 答案后作答；父回合走文本。
type escalateRoutedProvider struct {
	mu     sync.Mutex
	parent int
}

func (p *escalateRoutedProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	last := ""
	askReturned := false
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role == ai.RoleUser && last == "" {
			last = m.Text()
		}
		// ask 的回答是 toolResult 消息（不是用户文本）——扫块找。
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockToolResult && strings.Contains(b.Text, "用户回答") {
				askReturned = true
			}
		}
	}
	isSub := strings.Contains(last, "调查部署目标")
	isAskReturn := askReturned
	out := make(chan ai.StreamEvent, 8)
	go func() {
		defer close(out)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		switch {
		case isSub && !isAskReturn: // 代理首回合：发问
			out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "c1", ToolCallName: "ask"}
			out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "c1",
				ToolCallArgs: `{"question":"部署到预发还是生产？","options":["预发","生产"]}`}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
		case isAskReturn: // 答案已回转录：据此作答
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "将部署到预发"}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		default: // 父回合：先启动代理再收尾
			p.mu.Lock()
			p.parent++
			n := p.parent
			p.mu.Unlock()
			if n == 1 {
				out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "cp", ToolCallName: "agent"}
				out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "cp",
					ToolCallArgs: `{"prompt":"调查部署目标"}`}
				out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
			} else {
				out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "收工"}
				out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
			}
		}
	}()
	return out
}

func TestE2EAgentEscalatesQuestion(t *testing.T) {
	h := New(t)
	rp := &escalateRoutedProvider{}
	h.Svc.UseProviderForTesting(func() ai.Provider { return rp })
	t.Cleanup(func() { h.Svc.UseProviderForTesting(nil) })

	// 会话提问通道：记录升级的问题并回答「预发」。
	var mu sync.Mutex
	var escalated string
	h.Svc.SetQuestion(func(ctx context.Context, q agent.Question) (string, error) {
		mu.Lock()
		escalated = q.Text
		mu.Unlock()
		return "预发", nil
	})

	h.Ask("后台查一下部署") // 父回合 1：spawn ag-1

	// 等代理完成（升级问答在后台回合里同步完成）。
	if !waitFor(10e9, func() bool {
		for _, v := range h.Svc.AgentJobStates(h.SessionID) {
			if v.ID == "ag-1" && v.State == tools.AgentDone && v.Text == "将部署到预发" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("escalating agent never finished with the informed answer")
	}

	mu.Lock()
	got := escalated
	mu.Unlock()
	if !strings.Contains(got, "[后台代理 ag-1]") || !strings.Contains(got, "预发还是生产") {
		t.Fatalf("escalated question = %q", got)
	}
}
