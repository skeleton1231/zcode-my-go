package ai

import "strings"

// ModelInfo describes a known model: the catalog ZCode fetches at runtime,
// kept local here with a conservative fallback for unknown ids.
type ModelInfo struct {
	ID            string
	API           string // anthropic | openai | gemini ("" = any/unknown)
	ContextWindow int    // input+output tokens the provider allows
	MaxOutput     int    // the output cap requests should use
}

// DefaultContextWindow is the fallback for unknown models (ZCode's
// DEFAULT_COMPACT_CONTEXT_WINDOW).
const DefaultContextWindow = 200_000

// DefaultMaxOutput is the default output cap.
const DefaultMaxOutput = 32_000

var catalog = []ModelInfo{
	// Anthropic 兼容
	{ID: "claude-opus-4-5", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000},
	{ID: "claude-sonnet-4-5", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000},
	{ID: "claude-haiku-4-5", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000},
	{ID: "glm-5.2", API: "anthropic", ContextWindow: 200_000, MaxOutput: 32_000},
	{ID: "glm-5.2-air", API: "anthropic", ContextWindow: 200_000, MaxOutput: 16_000},
	{ID: "kimi-k2", API: "anthropic", ContextWindow: 256_000, MaxOutput: 32_000},
	// OpenAI 兼容
	{ID: "gpt-5.2", API: "openai", ContextWindow: 400_000, MaxOutput: 128_000},
	{ID: "gpt-5.2-mini", API: "openai", ContextWindow: 400_000, MaxOutput: 128_000},
	{ID: "deepseek-chat", API: "openai", ContextWindow: 128_000, MaxOutput: 16_000},
	{ID: "deepseek-reasoner", API: "openai", ContextWindow: 128_000, MaxOutput: 32_000},
	{ID: "qwen-max", API: "openai", ContextWindow: 131_072, MaxOutput: 16_000},
	{ID: "qwen3-coder-plus", API: "openai", ContextWindow: 1_000_000, MaxOutput: 32_000},
	// Gemini
	{ID: "gemini-3-pro", API: "gemini", ContextWindow: 1_000_000, MaxOutput: 64_000},
	{ID: "gemini-3-flash", API: "gemini", ContextWindow: 1_000_000, MaxOutput: 64_000},
}

// LookupModel finds a catalog entry by id (case-insensitive, suffix
// variants like dates fall back to the family entry).
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
	return ModelInfo{ID: id, ContextWindow: DefaultContextWindow, MaxOutput: DefaultMaxOutput}
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
