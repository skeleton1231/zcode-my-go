package pricing

// 牌价与成本核算单测（spec 规则 93）：覆盖优先、族前缀解析、
// 微美元算术、未知价诚实跳过。

import (
	"strings"
	"testing"
)

func TestLookupCatalogAndFamily(t *testing.T) {
	tab := New(nil)
	p, ok := tab.Lookup("claude-sonnet-4-5")
	if !ok || p.In != 3 || p.Out != 15 || p.CacheRead != 0.3 {
		t.Fatalf("sonnet price = %+v ok=%v", p, ok)
	}
	// 日期后缀变体走族解析。
	p, ok = tab.Lookup("Claude-Sonnet-4-5-20261001")
	if !ok || p.In != 3 {
		t.Fatalf("family variant = %+v ok=%v", p, ok)
	}
	// 未知模型（目录与覆盖都没有）：ok=false。
	if _, ok := tab.Lookup("totally-unknown"); ok {
		t.Fatal("unknown model reported known")
	}
	// 目录里 0 价的族（未定价）：也诚实 unknown。
	if _, ok := tab.Lookup("glm-5.2"); ok {
		t.Fatal("unpriced catalog entry should be unknown")
	}
}

func TestOverrideWins(t *testing.T) {
	tab := New(map[string]Override{
		"glm-5.2": {In: 0.5, Out: 2, CacheRead: 0.05},
	})
	p, ok := tab.Lookup("glm-5.2")
	if !ok || p.In != 0.5 || p.Out != 2 {
		t.Fatalf("override lost: %+v", p)
	}
	// 覆盖也按族前缀盖住变体。
	if p, ok = tab.Lookup("glm-5.2-air"); !ok || p.In != 0.5 {
		t.Fatalf("override family miss: %+v ok=%v", p, ok)
	}
	// 更长键优先：glm-5.2-air 的专属覆盖赢过 glm-5.2。
	tab2 := New(map[string]Override{
		"glm-5.2":      {In: 0.5},
		"glm-5.2-air":  {In: 0.1},
	})
	if p, _ = tab2.Lookup("glm-5.2-air"); p.In != 0.1 {
		t.Fatalf("longest key should win: %+v", p)
	}
}

func TestCostMicroArithmetic(t *testing.T) {
	p := Price{In: 3, Out: 15, CacheRead: 0.3}
	// 1M in + 1M out + 1M cacheRead = 3$ + 15$ + 0.3$ = 18.3$。
	got := CostMicro(p, 1_000_000, 1_000_000, 1_000_000)
	if got != 18_300_000 {
		t.Fatalf("CostMicro = %d", got)
	}
	// 零段自然 0。
	if CostMicro(p, 0, 0, 0) != 0 {
		t.Fatal("zero usage nonzero cost")
	}
	// 只算已知段（In=0 的价只算输出）。
	partial := CostMicro(Price{Out: 2}, 1_000_000, 1_000_000, 0)
	if partial != 2_000_000 {
		t.Fatalf("partial = %d", partial)
	}
}

func TestFormatUSD(t *testing.T) {
	if got := FormatUSD(18_300_000); got != "$18.30" {
		t.Fatalf("FormatUSD = %q", got)
	}
	if got := FormatUSD(0); got != "$0.00" {
		t.Fatalf("zero = %q", got)
	}
	if got := FormatUSD(1); !strings.HasPrefix(got, "$0.00") {
		t.Fatalf("micro rounding = %q", got)
	}
}
