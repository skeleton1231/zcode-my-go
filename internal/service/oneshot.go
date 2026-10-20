package service

// OneShot：单请求问答面（spec 规则 122 的 judge 评分用；与 compare 同
// 形态但不挑 profile——活动 provider 直接答）。无会话、无工具、无历
// 史：一句系统提示 + 一句用户输入 → 全量文本。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// errNoProvider 是未配置 provider 的明确错误（OneShot 面）。
var errNoProvider = errors.New("未配置 provider——zgo settings 配 API key 后可用")

// oneShotTimeout / oneShotMaxTokens：judge 回复只是一行 JSON——上限
// 收紧（成本与延迟双控）。
const (
	oneShotTimeout   = 90 * time.Second
	oneShotMaxTokens = 1024
)

// OneShot 用活动 provider 单请求回答。未配置 key（且无 OAuth）时明确
// 报错——调用方把它转成可读失败，不静默。
func (s *AgentService) OneShot(ctx context.Context, system, prompt string) (string, error) {
	cfg := s.resolve()
	var provider ai.Provider
	if s.providerOverride != nil {
		// e2e 注入实例级生效——OneShot 同语义（测试脚本回放）。
		provider = s.providerOverride()
	} else {
		if cfg.APIKey == "" && cfg.OAuth.AccessToken == "" {
			return "", errNoProvider
		}
		provider = s.providerFor(cfg)
	}
	var text strings.Builder
	for ev := range provider.Stream(ctx, ai.Request{
		Model:      cfg.Model,
		System:     system,
		MaxTokens:  oneShotMaxTokens,
		Messages:   []ai.Message{{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}},
	}) {
		switch ev.Kind {
		case ai.EvTextDelta:
			text.WriteString(ev.TextDelta)
		case ai.EvMessageEnd:
			if ev.StopReason == ai.StopError && ev.ErrorMessage != "" {
				return "", fmt.Errorf("provider: %s", ev.ErrorMessage)
			}
		}
	}
	return text.String(), nil
}
