package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDayAddLoadPriced 验证当日记账：增量追加、Load 重放求和（跨进程/
// 会话共享）、隔天文件按日期自然隔离、Priced 折算。
func TestDayAddLoadPriced(t *testing.T) {
	dir := t.TempDir()
	d := Load(dir)
	if d.Tokens != 0 {
		t.Fatalf("fresh day = %d", d.Tokens)
	}
	if err := d.Add(120); err != nil {
		t.Fatal(err)
	}
	if err := d.Add(30); err != nil {
		t.Fatal(err)
	}
	if d.Tokens != 150 {
		t.Fatalf("in-memory = %d", d.Tokens)
	}

	d2 := Load(dir) // 重放同一文件
	if d2.Tokens != 150 {
		t.Fatalf("replayed = %d", d2.Tokens)
	}

	// 记账文件名按日期。
	name := filepath.Base(d.path)
	if len(name) != len("usage-20060102.json") {
		t.Fatalf("file name = %q", name)
	}
	if _, err := os.Stat(d.path); err != nil {
		t.Fatalf("ledger file missing: %v", err)
	}
}

// TestCheckBudget 验证阈值提示各发一次：near→over→静默；0=不限。
func TestCheckBudget(t *testing.T) {
	d := &Day{}
	if got := d.CheckBudget(0); got != "" {
		t.Fatalf("unlimited budget notified: %q", got)
	}
	d.Tokens = 85
	if got := d.CheckBudget(100); got != "near" {
		t.Fatalf("80%% crossing = %q", got)
	}
	if got := d.CheckBudget(100); got != "" { // 只发一次
		t.Fatalf("repeated near: %q", got)
	}
	d.Tokens = 130
	if got := d.CheckBudget(100); got != "over" {
		t.Fatalf("100%% crossing = %q", got)
	}
	if got := d.CheckBudget(100); got != "" {
		t.Fatalf("repeated over: %q", got)
	}
}

// TestPriced 验证缓存读 0.1 计价。
func TestPriced(t *testing.T) {
	if got := Priced(100, 50, 500, 0); got != 200 {
		t.Fatalf("Priced = %d, want 200", got)
	}
}
