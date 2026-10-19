package usage

// 成本分量记账测试（spec 规则 93）：条目带模型与三段、旧文件零兼容
// 重放、当日/历史 $ 折算。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/pricing"
)

func TestAddSegmentsAndCostMicro(t *testing.T) {
	dir := t.TempDir()
	d := Load(dir)
	if err := d.AddSegments("s1", "main", "claude-sonnet-4-5",
		1_000_000, 1_000_000, 1_000_000, Priced(1_000_000, 1_000_000, 1_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	// token 面不受影响（规则 35 语义）。
	if d.Total() != 2_100_000 {
		t.Fatalf("tokens = %d", d.Total())
	}
	tab := pricing.New(nil)
	if got := d.CostMicro(tab); got != 18_300_000 {
		t.Fatalf("cost = %d micro (want 18.3$)", got)
	}
	// 未知模型条目贡献 0。
	if err := d.AddSegments("s1", "main", "mystery-model", 100, 100, 0, 200); err != nil {
		t.Fatal(err)
	}
	if got := d.CostMicro(tab); got != 18_300_000 {
		t.Fatalf("unknown model polluted cost: %d", got)
	}
	// nil 表 = 0（兼容面）。
	if d.CostMicro(nil) != 0 {
		t.Fatal("nil table nonzero")
	}
}

func TestLegacyEntriesReplayWithoutCost(t *testing.T) {
	dir := t.TempDir()
	// 规则 89 时代的旧格式行（无 model/分段）。
	legacy := `{"tokens":500,"session":"old"}` + "\n"
	path := filepath.Join(dir, "usage-"+time.Now().Format("20060102")+".json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	d := Load(dir)
	if d.Total() != 500 {
		t.Fatalf("legacy replay = %d", d.Total())
	}
	if d.CostMicro(pricing.New(nil)) != 0 {
		t.Fatal("legacy rows must cost nothing")
	}
	// 混合：新条目叠加旧账。
	if err := d.AddSegments("new", "main", "claude-haiku-4-5",
		1_000_000, 0, 0, 1_000_000); err != nil {
		t.Fatal(err)
	}
	// haiku in 1M = 1$。
	if got := d.CostMicro(pricing.New(nil)); got != 1_000_000 {
		t.Fatalf("mixed cost = %d", got)
	}
	if d.Total() != 1_000_500 {
		t.Fatalf("mixed tokens = %d", d.Total())
	}
}

func TestHistoryCarriesCost(t *testing.T) {
	dir := t.TempDir()
	d := Load(dir)
	if err := d.AddSegments("s", "main", "claude-opus-4-5",
		2_000_000, 0, 0, 2_000_000); err != nil {
		t.Fatal(err)
	} // opus in 2M = 30$
	hist := History(dir, 1, pricing.New(nil))
	if len(hist) != 1 {
		t.Fatalf("hist = %d", len(hist))
	}
	if hist[0].CostMicro != 30_000_000 {
		t.Fatalf("day cost = %d", hist[0].CostMicro)
	}
	if hist[0].Tokens != 2_000_000 {
		t.Fatalf("day tokens = %d", hist[0].Tokens)
	}
}
