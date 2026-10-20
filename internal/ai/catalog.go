package ai

import "strings"

// ModelInfo describes a known model: the catalog ZCode fetches at runtime,
// kept local here with a conservative fallback for unknown ids.
type ModelInfo struct {
	ID            string
	API           string // anthropic | openai | gemini ("" = any/unknown)
	ContextWindow int    // input+output tokens the provider allows
	MaxOutput     int    // the output cap requests should use
	// In/Out/CacheRead 是 $/Mtok 牌价（spec 规则 93）：内置值是可覆盖
	// 的近似（settings.pricing 优先）；0 = 未知——成本面跳过而不是瞎猜。
	InPrice        float64
	OutPrice       float64
	CacheReadPrice float64
}

// DefaultContextWindow is the fallback for unknown models (ZCode's
// DEFAULT_COMPACT_CONTEXT_WINDOW).
const DefaultContextWindow = 200_000

// DefaultMaxOutput is the default output cap.
const DefaultMaxOutput = 32_000

var catalog = []ModelInfo{
	// Anthropic 兼容
	{ID: "claude-opus-4-5", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000, InPrice: 15, OutPrice: 75, CacheReadPrice: 1.5},
	{ID: "claude-sonnet-4-5", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000, InPrice: 3, OutPrice: 15, CacheReadPrice: 0.3},
	{ID: "claude-haiku-4-5", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000, InPrice: 1, OutPrice: 5, CacheReadPrice: 0.1},
	{ID: "glm-5.2", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000},
	{ID: "glm-5.2-air", API: "anthropic", ContextWindow: 200_000, MaxOutput: 16_000},
	{ID: "kimi-k2", API: "anthropic", ContextWindow: 256_000, MaxOutput: 32_000, InPrice: 0.6, OutPrice: 2.5, CacheReadPrice: 0.1},
	// OpenAI 兼容
	{ID: "gpt-5.2", API: "openai", ContextWindow: 400_000, MaxOutput: 128_000},
	{ID: "gpt-5.2-mini", API: "openai", ContextWindow: 400_000, MaxOutput: 128_000},
	{ID: "deepseek-chat", API: "openai", ContextWindow: 128_000, MaxOutput: 16_000, InPrice: 0.27, OutPrice: 1.1, CacheReadPrice: 0.07},
	{ID: "deepseek-reasoner", API: "openai", ContextWindow: 128_000, MaxOutput: 32_000, InPrice: 0.55, OutPrice: 2.19, CacheReadPrice: 0.14},
	{ID: "qwen-max", API: "openai", ContextWindow: 131_072, MaxOutput: 16_000},
	{ID: "qwen3-coder-plus", API: "openai", ContextWindow: 1_000_000, MaxOutput: 32_000},
	// Gemini
	{ID: "gemini-3-pro", API: "gemini", ContextWindow: 1_000_000, MaxOutput: 64_000},
	{ID: "gemini-3-flash", API: "gemini", ContextWindow: 1_000_000, MaxOutput: 64_000},
}

// LookupModel finds a catalog entry by id (case-insensitive, suffix
// variants like dates fall back to the family entry). 查询顺序：内置目录
// （带牌价/窗口）→ 在线刷新覆盖层（spec 规则 125，只补内置没有的
// id）→ 默认值兜底。
func LookupModel(id string) ModelInfo {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, m := range catalog {
		if strings.EqualFold(m.ID, id) {
			return m
		}
	}
	// 无日期后缀的族名匹配：claude-sonnet-4-5-20261001 → claude-sonnet-4-5。
	// 最长前缀优先（issue #16）：glm-5.2 声明在 glm-5.2-air 之前，取首个
	// 命中会让 glm-5.2-air-* 错误解析到基础款（MaxOutput 32000≠16000）。
	var best ModelInfo
	for _, m := range catalog {
		if strings.HasPrefix(id, strings.ToLower(m.ID)) && len(m.ID) > len(best.ID) {
			best = m
		}
	}
	if best.ID != "" {
		return best
	}
	if m, ok := overlayLookup(id); ok { // 覆盖层：刷新进来的未知 id（前缀同语义）
		m.ID = id
		return m
	}
	return ModelInfo{ID: id, ContextWindow: DefaultContextWindow, MaxOutput: DefaultMaxOutput}
}

// KnownModel 报告 id 是否为目录模型或其家族变体（带日期后缀等）。
// 静态校验有本质限制：glm-5.2x 与合法日期后缀在前缀上不可区分，
// 但完全无家族前缀的拼写错误（如 cloude）可捕获（issue #61）。
// 覆盖层（spec 规则 125）同样参与判定——刷新过的 id 不再报未知。
func KnownModel(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	for _, m := range catalog {
		if strings.HasPrefix(id, strings.ToLower(m.ID)) {
			return true
		}
	}
	return overlayKnown(id)
}

// CatalogModels lists suggested models for one API dialect.
func CatalogModels(api string) []ModelInfo {
	var out []ModelInfo
	for _, m := range catalog {
		if m.API == api {
			out = append(out, m)
		}
	}
	return out
}
