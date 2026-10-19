// Package usage tracks the day's token spending across sessions（spec
// 规则 35）：dataDir 下的当日记账文件跨会话共享、进程外持久——个人
// 订阅用户对"今天烧了多少"的预算可见性。
package usage

import (
	"encoding/json"
	"github.com/skeleton1231/zcode-my-go/internal/pricing"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Day 记账结构（JSONL 追加，一条一次回合增量；读取时重放求和）。
type Day struct {
	mu   sync.Mutex
	dir  string // dataDir：翻日时按新日期重建 path
	path string
	day  string           // 当日 YYYYMMDD（进程内翻滚检测，issue #199）
	now  func() time.Time // 注入时钟（测试跨零点）；nil = time.Now

	Tokens int `json:"tokens"` // 今日累计（缓存读按 0.1 计价后的整数）
	// bySession/bySource 是归因聚合（spec 规则 89）：仅内存（重放自文
	// 件重建）——「哪个会话/来源烧了预算」的观测面。
	bySession map[string]int
	bySource  map[string]int
	// warned80/warned100 只在内存：进程重启（及翻日）后阈值可能重发
	// 一次——可接受（预算提示是化妆性信息，宁可多提醒）。
	warned80  bool
	warned100 bool
	// costSegs 是模型 → 三段分量累计（spec 规则 93）：$ 折算的原料，
	// 重放/翻日与 Tokens 同生命周期。
	costSegs map[string]segTotals
}

// segTotals 是一个模型的输入/输出/缓存读累计。
type segTotals struct {
	in, out, cacheRead int64
}

// entry 是一条增量记录。
type entry struct {
	Tokens int `json:"tokens"`
	// 归因（spec 规则 89）：旧文件无这两键照样重放（零值 = 未归因）。
	SessionID string `json:"session,omitempty"`
	Source    string `json:"source,omitempty"` // main/subagent/workflow
	// 成本分量（spec 规则 93）：$ 按条目实际模型的三段牌价折算——
	// 旧文件无这些键照样重放（成本 0，token 面不变）。
	Model     string `json:"model,omitempty"`
	In        int    `json:"in,omitempty"`
	Out       int    `json:"out,omitempty"`
	CacheRead int    `json:"cacheRead,omitempty"`
}

// Load 重放当日记账（文件按日期命名，隔天自然从零开始）。
func Load(dataDir string) *Day {
	return load(dataDir, nil)
}

// load 是 Load 的可注入时钟形态（测试翻日）。
func load(dataDir string, now func() time.Time) *Day {
	d := &Day{dir: dataDir, now: now, bySession: map[string]int{}, bySource: map[string]int{},
		costSegs: map[string]segTotals{}}
	d.rollLocked() // 依当前日期初始化 day/path
	data, err := os.ReadFile(d.path)
	if err != nil {
		return d
	}
	for _, line := range splitLines(data) {
		var e entry
		if json.Unmarshal(line, &e) == nil {
			d.Tokens += e.Tokens
			d.bySession[e.SessionID] += e.Tokens
			d.bySource[e.Source] += e.Tokens
			if e.Model != "" {
				seg := d.costSegs[e.Model]
				seg.in += int64(e.In)
				seg.out += int64(e.Out)
				seg.cacheRead += int64(e.CacheRead)
				d.costSegs[e.Model] = seg
			}
		}
	}
	return d
}

// clock 返回当前时间（可注入）。
func (d *Day) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// rollLocked 检测日期翻滚并切换：清零计数、指向新文件、重置 warned
// 标志（issue #199：桌面进程连跑数天是常态，Load 定死日期会让跨零点
// 后的开销全记旧日、守卫读数失真；旧文件留在原地即历史）。调用方持锁。
func (d *Day) rollLocked() {
	today := d.clock().Format("20060102")
	if today == d.day {
		return
	}
	d.day = today
	d.path = filepath.Join(d.dir, "usage-"+today+".json")
	d.Tokens = 0
	d.costSegs = map[string]segTotals{}
	d.bySession = map[string]int{}
	d.bySource = map[string]int{}
	d.warned80, d.warned100 = false, false
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// Add 记一次未归因增量（兼容面）。
func (d *Day) Add(tokens int) error { return d.AddFor("", "", tokens) }

// AddFor 记一次归因增量并落盘（spec 规则 89）：sessionID 空即未归
// 因；source ∈ main/subagent/workflow。
func (d *Day) AddFor(sessionID, source string, tokens int) error {
	return d.AddSegments(sessionID, source, "", 0, 0, 0, tokens)
}

// AddSegments 记一次带成本分量的增量（spec 规则 93）：tokens 仍是计
// 价口径（调用方 Priced 折算），in/out/cacheRead 是原始三段、model 是
// 实际模型——日账重放时按条目模型折算 $。
func (d *Day) AddSegments(sessionID, source, model string, in, out, cacheRead, tokens int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rollLocked() // 跨零点：先翻到新日再记账
	d.Tokens += tokens
	d.bySession[sessionID] += tokens
	d.bySource[source] += tokens
	if model != "" {
		seg := d.costSegs[model]
		seg.in += int64(in)
		seg.out += int64(out)
		seg.cacheRead += int64(cacheRead)
		d.costSegs[model] = seg
	}
	line, err := json.Marshal(entry{Tokens: tokens, SessionID: sessionID, Source: source,
		Model: model, In: in, Out: out, CacheRead: cacheRead})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(d.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// CostMicro 按各模型实际牌价折算当日记账成本（微美元，spec 规则
// 93）；t 为 nil 或模型未知时该模型贡献 0。
func (d *Day) CostMicro(t *pricing.Table) int64 {
	if t == nil {
		return 0
	}
	d.mu.Lock()
	segs := make(map[string]segTotals, len(d.costSegs))
	for k, v := range d.costSegs {
		segs[k] = v
	}
	d.mu.Unlock()
	var total int64
	for model, seg := range segs {
		p, ok := t.Lookup(model)
		if !ok {
			continue
		}
		total += pricing.CostMicro(p, seg.in, seg.out, seg.cacheRead)
	}
	return total
}

// Priced 把一段用量折算为计价 token（缓存读 0.1——订阅计费口径的近
// 似，避免缓存读满额计入预算）。
func Priced(in, out, cacheRead, cacheWrite int) int {
	return in + out + cacheRead/10 + cacheWrite
}

// Total 报告今日累计（持锁——Add 会从多会话的回合收尾 goroutine 并发
// 到达，裸读 Tokens 与之竞态；翻日在锁内一并检测）。
func (d *Day) Total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rollLocked()
	return d.Tokens
}

// CheckBudget 返回（80%/100% 阈值跨越提示，各只发一次）。
// budget<=0 不限；返回空串表示无提示。
func (d *Day) CheckBudget(budget int) string {
	if budget <= 0 {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rollLocked()
	switch {
	case d.Tokens >= budget && !d.warned100:
		d.warned100 = true
		return "over"
	case d.Tokens >= budget*8/10 && !d.warned80:
		d.warned80 = true
		return "near"
	}
	return ""
}

// SessionTotal 是单会话的今日用量。
type SessionTotal struct {
	SessionID string
	Tokens    int
}

// TopSessions 返回今日按会话聚合的用量（降序；未归因条目归 ""）。
func (d *Day) TopSessions() []SessionTotal {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]SessionTotal, 0, len(d.bySession))
	for id, tokens := range d.bySession {
		out = append(out, SessionTotal{SessionID: id, Tokens: tokens})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tokens > out[j].Tokens })
	return out
}

// SourceTotal 返回单一来源（main/subagent/workflow）的今日用量。
func (d *Day) SourceTotal(source string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bySource[source]
}

// DaySummary 是历史某日的用量摘要（spec 规则 90）。
type DaySummary struct {
	Day    string // YYYYMMDD
	Tokens int
	// CostMicro 是当日成本（微美元，spec 规则 93；无牌价条目贡献 0）。
	CostMicro int64
}

// History 重放最近 days 天的日账文件（含今日），旧到新排序。usage-*.json
// 翻日后留在原地即历史（rollLocked 的既有语义）——只读聚合，不改 Day
// 状态。缺日跳过（没跑就没有账）。
func History(dataDir string, days int, t *pricing.Table) []DaySummary {
	if days <= 0 {
		days = 7
	}
	var out []DaySummary
	today := time.Now()
	for i := days - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("20060102")
		raw, err := os.ReadFile(filepath.Join(dataDir, "usage-"+day+".json"))
		if err != nil {
			out = append(out, DaySummary{Day: day}) // 缺日照列（趋势连续性）
			continue
		}
		total := 0
		var cost int64
		for _, line := range splitLines(raw) {
			var e entry
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			total += e.Tokens
			if t != nil && e.Model != "" {
				if p, ok := t.Lookup(e.Model); ok {
					cost += pricing.CostMicro(p, int64(e.In), int64(e.Out), int64(e.CacheRead))
				}
			}
		}
		out = append(out, DaySummary{Day: day, Tokens: total, CostMicro: cost})
	}
	return out
}
