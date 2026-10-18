// Package usage tracks the day's token spending across sessions（spec
// 规则 35）：dataDir 下的当日记账文件跨会话共享、进程外持久——个人
// 订阅用户对"今天烧了多少"的预算可见性。
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Day 记账结构（JSONL 追加，一条一次回合增量；读取时重放求和）。
type Day struct {
	mu   sync.Mutex
	path string

	Tokens int `json:"tokens"` // 今日累计（缓存读按 0.1 计价后的整数）
	// warned80/warned100 只在内存：进程重启后阈值可能重发一次——可
	// 接受（预算提示是化妆性信息，宁可多提醒）。
	warned80  bool
	warned100 bool
}

// entry 是一条增量记录。
type entry struct {
	Tokens int `json:"tokens"`
}

// Load 重放当日记账（文件按日期命名，隔天自然从零开始）。
func Load(dataDir string) *Day {
	d := &Day{path: filepath.Join(dataDir, "usage-"+time.Now().Format("20060102")+".json")}
	data, err := os.ReadFile(d.path)
	if err != nil {
		return d
	}
	for _, line := range splitLines(data) {
		var e entry
		if json.Unmarshal(line, &e) == nil {
			d.Tokens += e.Tokens
		}
	}
	return d
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

// Add 记一次回合增量并落盘（追加一行）。
func (d *Day) Add(tokens int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Tokens += tokens
	line, err := json.Marshal(entry{Tokens: tokens})
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

// Priced 把一段用量折算为计价 token（缓存读 0.1——订阅计费口径的近
// 似，避免缓存读满额计入预算）。
func Priced(in, out, cacheRead, cacheWrite int) int {
	return in + out + cacheRead/10 + cacheWrite
}

// CheckBudget 返回（80%/100% 阈值跨越提示，各只发一次）。
// budget<=0 不限；返回空串表示无提示。
func (d *Day) CheckBudget(budget int) string {
	if budget <= 0 {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
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
