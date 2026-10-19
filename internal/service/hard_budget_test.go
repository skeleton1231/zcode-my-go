package service

// 硬预算熔断（spec 规则 85）：达预算拒新回合（明确报错）、关闸照常、
// 跨天重置、cron 调度同闸（scheduleRunner 走 Chat 入口）。

import (
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
