package service

// 多模型对比（spec 规则 104）：同一个问题并行问 N 个 profile，并排
// 呈现答案与用量/成本——选型与评审场景。每侧是一次单请求（无工具、
// 无转录），MaxTokens 封顶防失控；失败的侧如实带错误（不拖垮别的）。

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/pricing"
)

// compareMaxTokens 是单侧输出的封顶（对比是短答场景，不是跑任务）。
const compareMaxTokens = 2048

// compareTimeout 兜底单侧时长（provider 各自还有流级超时）。
const compareTimeout = 120 * time.Second

// CompareResult 是一个 profile 侧的答案。
type CompareResult struct {
	Profile string
	Model   string
	Text    string
	Err     string
	Usage   ai.Usage
	// CostMicro 是该侧成本（未知牌价 0）。
	CostMicro int64
	Duration  time.Duration
}

// profileProviderFor 是 profile→provider 的构建缝（生产走 providerFor；
// 测试注入脚本 provider——profile 侧不经 providerOverride 注入面）。
var profileProviderFor = func(s *AgentService, cfg settings) ai.Provider {
	return s.providerFor(cfg)
}

// CompareProfiles 把 prompt 并行发给命名的 profiles（空切片 = 全部
// profile），返回按入参序的结果。没有可用 profile 报错（提示 /provider
// 配置）。
func (s *AgentService) CompareProfiles(prompt string, names []string) ([]CompareResult, error) {
	s.mu.Lock()
	profiles := map[string]profile{}
	for k, v := range s.settings.Profiles {
		profiles[k] = v
	}
	s.mu.Unlock()
	if len(profiles) == 0 {
		return nil, nil
	}
	if len(names) == 0 {
		for k := range profiles {
			names = append(names, k)
		}
	}
	var ordered []string
	for _, n := range names {
		if _, ok := profiles[strings.TrimSpace(n)]; ok {
			ordered = append(ordered, strings.TrimSpace(n))
		}
	}
	if len(ordered) == 0 {
		return nil, nil
	}

	results := make([]CompareResult, len(ordered))
	var wg sync.WaitGroup
	for i, name := range ordered {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			results[i] = s.compareOne(prompt, name, profiles[name])
		}(i, name)
	}
	wg.Wait()
	return results, nil
}

// compareOne 跑一个 profile 侧：单请求、收全量文本、记录用量与成本。
func (s *AgentService) compareOne(prompt, name string, p profile) CompareResult {
	res := CompareResult{Profile: name, Model: p.Model}
	started := time.Now()
	s.mu.Lock()
	cfg := settings{
		API: p.API, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Model: p.Model, Thinking: p.Thinking,
	}
	s.mu.Unlock()
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	provider := profileProviderFor(s, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), compareTimeout)
	defer cancel()
	var text strings.Builder
	var usage ai.Usage
	msg := ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}
	for ev := range provider.Stream(ctx, ai.Request{
		Model:     cfg.Model,
		MaxTokens: compareMaxTokens,
		Thinking:  thinkingLevelOf(cfg.Thinking),
		Messages:  []ai.Message{msg},
	}) {
		switch ev.Kind {
		case ai.EvTextDelta:
			text.WriteString(ev.TextDelta)
		case ai.EvMessageEnd:
			usage = ev.Usage
			if ev.StopReason == ai.StopError && ev.ErrorMessage != "" {
				res.Err = ev.ErrorMessage
			}
		}
	}
	res.Text = text.String()
	res.Usage = usage
	res.Duration = time.Since(started)
	if price, ok := pricing.New(nil).Lookup(cfg.Model); ok {
		res.CostMicro = pricing.CostMicro(price,
			int64(usage.InputTokens), int64(usage.OutputTokens), int64(usage.CacheReadTokens))
	}
	return res
}
