// Package pricing 是模型牌价与成本核算（spec 规则 93）：目录内置近似
// 价（$/Mtok，0=未知），settings.pricing 覆盖优先；成本以微美元整数
// 累加（避免浮点误差放大），展示时格式化。未知价的模型诚实跳过成本
// 行——不猜。
package pricing

import (
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// Price 是一族模型的 $/Mtok 牌价。
type Price struct {
	In        float64 `json:"in"`
	Out       float64 `json:"out"`
	CacheRead float64 `json:"cacheRead"`
}

// Known 报告牌价是否可用（任一段非零即可估算）。
func (p Price) Known() bool { return p.In > 0 || p.Out > 0 }

// Override 是 settings.pricing 的条目（与 Price 同构，独立类型保设置
// 文件的语义清晰）。
type Override struct {
	In        float64 `json:"in"`
	Out       float64 `json:"out"`
	CacheRead float64 `json:"cacheRead"`
}

// Table 合并目录与覆盖：覆盖键（小写）按最长前缀匹配模型 ID（与
// ai.LookupModel 的族名解析同语义——覆盖 claude-sonnet-4-5 也盖住
// -20261001 变体）。
type Table struct {
	overrides map[string]Price
}

// New 以设置覆盖构建（nil/空 = 纯目录）。
func New(overrides map[string]Override) *Table {
	t := &Table{overrides: map[string]Price{}}
	for k, v := range overrides {
		t.overrides[strings.ToLower(strings.TrimSpace(k))] = Price(v)
	}
	return t
}

// Lookup 解析模型的牌价；ok=false 表示未知（目录与覆盖都没有）。
func (t *Table) Lookup(model string) (Price, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if id == "" {
		return Price{}, false
	}
	// 覆盖优先：精确等值，退最长前缀（与目录族解析同语义）。
	if p, ok := t.longestPrefix(t.overrides, id); ok {
		return p, p.Known()
	}
	// 目录：借 ai.LookupModel 的族解析（返回带价的族条目）。
	m := ai.LookupModel(id)
	p := Price{In: m.InPrice, Out: m.OutPrice, CacheRead: m.CacheReadPrice}
	return p, p.Known()
}

func (t *Table) longestPrefix(m map[string]Price, id string) (Price, bool) {
	var best Price
	bestLen := -1
	for k, p := range m {
		if k == id || strings.HasPrefix(id, k) {
			if len(k) > bestLen {
				best, bestLen = p, len(k)
			}
		}
	}
	return best, bestLen >= 0
}

// CostMicro 按牌价折算微美元（1e-6 $）：in/out/cacheRead 三段分别计
// 价再取整段和。未知段（0 价）自然贡献 0。
func CostMicro(p Price, in, out, cacheRead int64) int64 {
	per := func(price float64, tokens int64) int64 {
		return int64(price * float64(tokens)) // $/Mtok × tokens = $ 的百万分之一
	}
	return per(p.In, in) + per(p.Out, out) + per(p.CacheRead, cacheRead)
}

// FormatUSD 微美元 → "$1.23"；0 显示 "$0.00"。
func FormatUSD(micro int64) string {
	return fmt.Sprintf("$%.2f", float64(micro)/1e6)
}
