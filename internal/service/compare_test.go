package service

// 多模型对比测试（spec 规则 104）：并行问 profiles、按名过滤、失败侧
// 不拖垮、无 profile nil、用量/成本入结果。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// cmpProvider 回固定文本（带 profile 模型名，可断言路由）。
type cmpProvider struct {
	model string
	fail  bool
}

func (p *cmpProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	if p.fail {
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError, ErrorMessage: "quota exceeded"}
	} else {
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "答案来自 " + req.Model}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
			Usage: ai.Usage{InputTokens: 100, OutputTokens: 50}}
	}
	close(out)
	return out
}

func cmpEnv(t *testing.T) (*AgentService, string) {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"profiles": map[string]any{
			"fast":   map[string]string{"model": "claude-haiku-4-5"},
			"smart":  map[string]string{"model": "claude-opus-4-5"},
			"broken": map[string]string{"model": "broken-model"},
		},
	})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	// 注入构建缝：按模型名路由（broken 侧失败）。
	old := profileProviderFor
	profileProviderFor = func(s *AgentService, cfg settings) ai.Provider {
		return &cmpProvider{model: cfg.Model, fail: strings.Contains(cfg.Model, "broken")}
	}
	t.Cleanup(func() { profileProviderFor = old })
	return svc, dir
}

func TestCompareProfiles(t *testing.T) {
	svc, _ := cmpEnv(t)
	results, err := svc.CompareProfiles("哪个方案好", []string{"fast", "smart", "broken"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d", len(results))
	}
	byName := map[string]CompareResult{}
	for _, r := range results {
		byName[r.Profile] = r
	}
	if got := byName["fast"].Text; got != "答案来自 claude-haiku-4-5" {
		t.Fatalf("fast text = %q", got)
	}
	if got := byName["smart"].Text; got != "答案来自 claude-opus-4-5" {
		t.Fatalf("smart text = %q", got)
	}
	if r := byName["broken"]; r.Err == "" || r.Text != "" {
		t.Fatalf("broken side = %+v", r)
	}
	// 成本：haiku 100 in + 50 out = 100µ$ + 250µ$ = 350µ$。
	if c := byName["fast"].CostMicro; c != 350 {
		t.Fatalf("fast cost = %d", c)
	}
	// 未知牌价的模型成本 0（不猜）。
	if c := byName["broken"].CostMicro; c != 0 {
		t.Fatalf("unknown model cost = %d", c)
	}
}

func TestCompareProfilesFilters(t *testing.T) {
	svc, _ := cmpEnv(t)
	// 全部 profile（空切片）。
	results, err := svc.CompareProfiles("x", nil)
	if err != nil || len(results) != 3 {
		t.Fatalf("all profiles = %d %v", len(results), err)
	}
	// 名字过滤。
	results, _ = svc.CompareProfiles("x", []string{"fast"})
	if len(results) != 1 || results[0].Profile != "fast" {
		t.Fatalf("filtered = %+v", results)
	}
	// 不存在的名字 → 空切片（CLI 层给提示）。
	results, _ = svc.CompareProfiles("x", []string{"nope"})
	if len(results) != 0 {
		t.Fatalf("unknown = %+v", results)
	}
	// 空设置服务：迁移保证有 default profile（#215）——对比跑它。
	bare, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	r, err := bare.CompareProfiles("x", nil)
	if err != nil || len(r) != 1 || r[0].Profile != "default" {
		t.Fatalf("bare = %v %v", r, err)
	}
}
