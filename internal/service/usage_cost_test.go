package service

// 成本面穿线测试（spec 规则 93）：真回合后日账条目带模型与三段、
// 会话成本行与当日 $ 归因可见；未知价模型诚实省略。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// costScriptProvider 每回合回带用量的文本。
type costScriptProvider struct {
	mu    sync.Mutex
	usage ai.Usage
}

func (p *costScriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu.Lock()
	p.mu.Unlock()
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "ok"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
		Usage: ai.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}}
	close(out)
	return out
}

func costEnv(t *testing.T, model string) (*AgentService, string) {
	t.Helper()
	dir := t.TempDir()
	if model != "" {
		raw, _ := json.Marshal(map[string]string{"model": model})
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := New(dir)
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
	svc.UseProviderForTesting(func() ai.Provider { return &costScriptProvider{} })
	return svc, info.ID
}

func TestUsageCostVisibleAfterTurn(t *testing.T) {
	svc, id := costEnv(t, "claude-sonnet-4-5")
	if err := svc.Chat(id, "跑一回合", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// 日账 $（条目模型穿线）：sonnet 1M/1M/1M = 3+15+0.3 = 18.3$。
	if got := svc.UsageCostToday(); got != 18_300_000 {
		t.Fatalf("today cost = %d micro", got)
	}
	// 会话成本行。
	sum, err := svc.UsageSummaryOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.CostKnown || sum.Model != "claude-sonnet-4-5" || sum.CostMicro != 18_300_000 {
		t.Fatalf("summary cost = %+v", sum)
	}
	block := FormatUsage(sum)
	if !strings.Contains(block, "$18.30") || !strings.Contains(block, "≈ 成本") {
		t.Fatalf("FormatUsage missing cost line:\n%s", block)
	}
	// 历史面同值。
	hist := svc.UsageHistory(1)
	if len(hist) != 1 || hist[0].CostMicro != 18_300_000 {
		t.Fatalf("history cost = %+v", hist)
	}
}

func TestUsageCostUnknownModelHidesLine(t *testing.T) {
	svc, id := costEnv(t, "totally-unknown-model")
	if err := svc.Chat(id, "跑一回合", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// 未知价：$ 面诚实为 0/省略，token 面照常。
	if got := svc.UsageCostToday(); got != 0 {
		t.Fatalf("unknown model cost = %d", got)
	}
	sum, err := svc.UsageSummaryOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if sum.CostKnown {
		t.Fatal("unknown model reported known")
	}
	if strings.Contains(FormatUsage(sum), "≈ 成本") {
		t.Fatalf("cost line leaked:\n%s", FormatUsage(sum))
	}
}

func TestPricingOverrideFlows(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{
		"model":   "glm-5.2",
		"pricing": map[string]any{"glm-5.2": map[string]float64{"in": 0.5, "out": 2}},
	})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
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
	svc.UseProviderForTesting(func() ai.Provider { return &costScriptProvider{} })
	if err := svc.Chat(info.ID, "跑一回合", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// glm 覆盖价：1M in + 1M out + 1M cacheRead(0 价) = 0.5 + 2 = 2.5$。
	if got := svc.UsageCostToday(); got != 2_500_000 {
		t.Fatalf("override cost = %d micro", got)
	}
}
