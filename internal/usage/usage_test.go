package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	d := Load(t.TempDir()) // 经构造器（零值 Day 首次使用会翻滚清零）
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

// TestDayRolloverMidnight: fake 时钟跨零点——Add 落新文件、计数清零、
// warned 标志重置、旧文件原样（issue #199：Load 定死日期，长跑桌面进程
// 跨零点后开销全记旧日、守卫失真）。
func TestDayRolloverMidnight(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 18, 23, 59, 0, 0, time.Local)
	d := load(dir, func() time.Time { return now })
	if err := d.Add(100); err != nil {
		t.Fatal(err)
	}
	if d.Total() != 100 {
		t.Fatalf("total = %d, want 100", d.Total())
	}
	old := filepath.Join(dir, "usage-20261018.json")
	if _, err := os.Stat(old); err != nil {
		t.Fatal("old day file missing")
	}
	if got := d.CheckBudget(125); got != "near" { // 100/125 = 80%
		t.Fatalf("pre-rollover check = %q, want near", got)
	}

	now = now.Add(2 * time.Minute) // 跨零点
	if err := d.Add(50); err != nil {
		t.Fatal(err)
	}
	if d.Total() != 50 {
		t.Fatalf("total = %d, want 50 after rollover", d.Total())
	}
	data, err := os.ReadFile(filepath.Join(dir, "usage-20261019.json"))
	if err != nil || !strings.Contains(string(data), "50") {
		t.Fatalf("new day file = %q, %v", data, err)
	}
	oldd, _ := os.ReadFile(old)
	if strings.Contains(string(oldd), "50") {
		t.Fatal("post-midnight spend leaked into the old day file")
	}
	// warned 标志随翻滚重置：新一日 50/60 ≥ 80% 再次可触。
	if got := d.CheckBudget(60); got != "near" {
		t.Fatalf("post-rollover check = %q, want near", got)
	}
}

// TestAttribution（spec 规则 89）：归因增量聚合按会话/按来源；旧文件
// 无归因键照样重放；翻日清聚合。
func TestAttribution(t *testing.T) {
	dir := t.TempDir()
	d := Load(dir)
	if err := d.AddFor("s1", "main", 100); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFor("s1", "subagent", 300); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFor("s2", "workflow", 50); err != nil {
		t.Fatal(err)
	}
	if err := d.Add(10); err != nil { // 未归因
		t.Fatal(err)
	}
	if got := d.Total(); got != 460 {
		t.Fatalf("total = %d", got)
	}
	tops := d.TopSessions()
	if len(tops) != 3 || tops[0].SessionID != "s1" || tops[0].Tokens != 400 {
		t.Fatalf("top sessions = %+v", tops)
	}
	if d.SourceTotal("main") != 100 || d.SourceTotal("subagent") != 300 || d.SourceTotal("workflow") != 50 {
		t.Fatal("source totals wrong")
	}

	// 重放：聚合自文件重建。
	d2 := Load(dir)
	if d2.SourceTotal("subagent") != 300 || d2.TopSessions()[0].Tokens != 400 {
		t.Fatalf("replay lost attribution: %+v", d2.TopSessions())
	}

	// 旧文件（无归因键）重放为零值键——总量不减。
	old := filepath.Join(dir, "usage-"+time.Now().Format("20060102")+".json")
	f, _ := os.OpenFile(old, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintln(f, `{"tokens":7}`)
	f.Close()
	d3 := Load(dir)
	if d3.Total() != 467 || d3.SourceTotal("") != 17 {
		t.Fatalf("legacy replay: total=%d unattributed=%d", d3.Total(), d3.SourceTotal(""))
	}
}

// TestHistory（spec 规则 90）：按日重放（含缺日占位、未来日不入、
// 归因键无所谓——历史只聚合总量）。
func TestHistory(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("20060102")
	yest := time.Now().AddDate(0, 0, -1).Format("20060102")
	older := time.Now().AddDate(0, 0, -2).Format("20060102") // 3 天窗的最早日
	for name, lines := range map[string][]string{
		"usage-" + today + ".json": {`{"tokens":100,"session":"s1"}`, `{"tokens":50}`},
		"usage-" + yest + ".json":  {`{"tokens":200}`},
		"usage-" + older + ".json": {`{"tokens":400}`},
		"usage-20200101.json":      {`{"tokens":9999}`}, // 窗口外：不入
	} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			fmt.Fprintln(f, l)
		}
		f.Close()
	}
	hist := History(dir, 3)
	if len(hist) != 3 {
		t.Fatalf("len = %d", len(hist))
	}
	if hist[0].Day != older || hist[0].Tokens != 400 {
		t.Fatalf("oldest = %+v", hist[0])
	}
	if hist[1].Day != yest || hist[1].Tokens != 200 {
		t.Fatalf("yesterday = %+v", hist[1])
	}
	if hist[2].Day != today || hist[2].Tokens != 150 {
		t.Fatalf("today = %+v", hist[2])
	}
	// 缺日占位。
	hist3 := History(dir, 5)
	if len(hist3) != 5 || hist3[0].Tokens != 0 {
		t.Fatalf("5-day = %+v", hist3)
	}
}
