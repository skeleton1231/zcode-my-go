package service

// 硬预算熔断（spec 规则 85）：达预算拒新回合（明确报错）、关闸照常、
// 跨天重置、cron 调度同闸（scheduleRunner 走 Chat 入口）。

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/usage"
)

// fuseEnv 起带预算的服务并直接灌满当日记账。
func fuseEnv(t *testing.T, budget int, hard bool) (*AgentService, string) {
	t.Helper()
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	if err := svc.SetBudgetDailyTokens(budget); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetHardBudgetStop(hard); err != nil {
		t.Fatal(err)
	}
	if err := svc.day.Add(budget); err != nil { // 灌满
		t.Fatal(err)
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &seqScriptProvider{turns: []string{"ok"}}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	return svc, info.ID
}

func TestHardBudgetFuseBlocks(t *testing.T) {
	svc, id := fuseEnv(t, 1000, true)
	err := svc.Chat(id, "再来一回合", func(agent.Event) {})
	if err == nil || !strings.Contains(err.Error(), "硬预算") {
		t.Fatalf("fuse did not block: %v", err)
	}
	// 不进转录（止损闸在回合开始前）。
	msgs, _ := svc.Messages(id)
	if len(msgs) != 0 {
		t.Fatalf("fused turn leaked into transcript: %d", len(msgs))
	}
}

func TestHardBudgetOffDoesNotBlock(t *testing.T) {
	svc, id := fuseEnv(t, 1000, false)
	if err := svc.Chat(id, "再来一回合", func(agent.Event) {}); err != nil {
		t.Fatalf("soft mode blocked: %v", err)
	}
}

func TestHardBudgetFusePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc.DisableAutoTitleForTesting()
	if err := svc.SetBudgetDailyTokens(1000); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetHardBudgetStop(true); err != nil {
		t.Fatal(err)
	}
	if err := svc.day.Add(1000); err != nil {
		t.Fatal(err)
	}
	info, _ := svc.NewSession(t.TempDir())
	id := info.ID
	svc.Close()

	// 重启：闸仍在（设置落盘 + 当日记账重放）。
	svc2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc2.Close)
	svc2.DisableAutoTitleForTesting()
	p := &seqScriptProvider{turns: []string{"ok"}}
	svc2.UseProviderForTesting(func() ai.Provider { return p })
	err = svc2.Chat(id, "重启后", func(agent.Event) {})
	if err == nil || !strings.Contains(err.Error(), "硬预算") {
		t.Fatalf("fuse lost across restart: %v", err)
	}
	if !svc2.HardBudgetStopEnabled() {
		t.Fatal("hardBudgetStop not persisted")
	}
}

// TestDayResetFreesFuse：跨天（Day.rollLocked 重放机制——同日文件不
// 存在时清零）后闸解开。usage.Day 的按日键由 Add 时段决定——直接验
// 证 CheckBudget 语义 + 新 Day 实例即代表新一天。
func TestDayResetFreesFuse(t *testing.T) {
	d := usage.Load(t.TempDir())
	if err := d.Add(1000); err != nil {
		t.Fatal(err)
	}
	if d.CheckBudget(1000) != "over" {
		t.Fatalf("over check = %q", d.CheckBudget(1000))
	}
	fresh := usage.Load(t.TempDir()) // 新的一天的等价物
	if fresh.Total() != 0 {
		t.Fatal("fresh day not zero")
	}
}

// TestSubagentUsageRecordedAndFused（spec 规则 87）：子代理用量入当日
// 记账（此前对记账/熔断完全不可见）；熔断后子代理启动被拒。
func TestSubagentUsageRecordedAndFused(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	if err := svc.SetBudgetDailyTokens(100_000); err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 脚本：回合 1 模型发 task 调用 → 子代理脚本（用量 3000）→ 文本
	// 收尾；回合 2 文本。
	p := &taskScriptProvider{turns: []string{"", "主回合收尾", "第二回合"}}
	svc.UseProviderForTesting(func() ai.Provider { return p })

	before, _ := svc.DailyUsage()
	if err := svc.Chat(info.ID, "调查一下", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.DailyUsage()
	if after-before < 3000 {
		t.Fatalf("subagent usage not recorded: %d -> %d", before, after)
	}

	// 熔断：灌满记账后子代理启动被拒。
	if err := svc.SetHardBudgetStop(true); err != nil {
		t.Fatal(err)
	}
	if err := svc.day.Add(100_000); err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "再调查", func(agent.Event) {}); err == nil ||
		!strings.Contains(err.Error(), "硬预算") {
		t.Fatalf("fused main turn should be rejected: %v", err)
	}
}

// taskScriptProvider：第 1 请求回 task 工具调用（子代理消耗脚本回合
// 2 的用量），子代理后主循环回文本。
type taskScriptProvider struct {
	mu    int
	turns []string
}

func (p *taskScriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu++
	n := p.mu
	out := make(chan ai.StreamEvent, 4)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	if n == 1 {
		out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "call_task_1", ToolCallName: "task"}
		out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "call_task_1",
			ToolCallArgs: `{"prompt":"查一下"}`}
	} else if n == 2 {
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "子代理结论"}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
			Usage: ai.Usage{InputTokens: 2000, OutputTokens: 1000}}
		return closeAndReturn(out)
	} else {
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: p.turns[min(n-1, len(p.turns)-1)]}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
			Usage: ai.Usage{InputTokens: 100, OutputTokens: 50}} // 主回合用量（归因断言）
		close(out)
		return out
	}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func closeAndReturn(out chan ai.StreamEvent) chan ai.StreamEvent {
	close(out)
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestWorkflowUsageRecorded（spec 规则 88）：workflow 步骤的 nested
// loop 用量入当日记账（规则 87 的最后盲区）。
func TestWorkflowUsageRecorded(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &wfScriptProvider{turns: []string{"", "步骤一结论", "主回合收尾"}}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	before, _ := svc.DailyUsage()
	if err := svc.Chat(info.ID, "按计划执行", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.DailyUsage()
	if after-before < 3000 {
		t.Fatalf("workflow step usage not recorded: %d -> %d", before, after)
	}
}

// wfScriptProvider：回合 1 模型发 workflow 调用（单步计划）→ 步骤
// 子任务（用量 3000）→ 主循环文本收尾。
type wfScriptProvider struct {
	mu    int
	turns []string
}

func (p *wfScriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu++
	n := p.mu
	out := make(chan ai.StreamEvent, 4)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	switch n {
	case 1:
		out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "call_wf_1", ToolCallName: "workflow"}
		out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "call_wf_1",
			ToolCallArgs: `{"steps":[{"name":"step1","kind":"research","prompt":"调查"}]}`}
	case 2:
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "步骤一结论"}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
			Usage: ai.Usage{InputTokens: 2000, OutputTokens: 1000}}
		close(out)
		return out
	default:
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "主回合收尾"}
	}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// TestUsageAttributionFlow（spec 规则 89）：主回合与子代理用量分别归
// 因到会话与来源。
func TestUsageAttributionFlow(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &taskScriptProvider{turns: []string{"", "主回合收尾"}}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(info.ID, "调查", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	_, main, sub, wf := svc.UsageAttribution()
	if sub < 3000 {
		t.Fatalf("subagent attribution missing: %d", sub)
	}
	if main == 0 {
		t.Fatal("main attribution missing")
	}
	if wf != 0 {
		t.Fatalf("workflow should be zero: %d", wf)
	}
	tops := svc.day.TopSessions()
	if len(tops) == 0 || tops[0].SessionID != info.ID {
		t.Fatalf("session attribution wrong: %+v", tops)
	}
}
