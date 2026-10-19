// Package audit 是工具执行的审计日志（spec 规则 100）：与遥测（规则
// 91）正交——遥测是性能维度（span、无参数），审计是安全合规维度
//（参数摘要 + 成败 + 耗时）。默认关；开启后每次工具执行追加一条
// JSONL 到 dataDir/audit/audit-YYYYMMDD.jsonl。红线同规则 73：参数摘
// 要过密钥打码——bash 命令全文是审计核心，但密钥永不落盘。
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/redact"
)

// Log 是审计面：并发安全（并行工具批次），按日追加。
type Log struct {
	mu  sync.Mutex
	dir string
}

// New 构建审计日志（dir = dataDir/audit）。
func New(dataDir string) *Log {
	return &Log{dir: filepath.Join(dataDir, "audit")}
}

// Entry 是一条工具执行记录。
type Entry struct {
	Time      time.Time `json:"time"`
	SessionID string    `json:"session,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	Tool      string    `json:"tool"`
	// Args 是参数摘要（审计核心：bash 记命令全文、write/edit/patch 记
	// 目标路径；内容体不记——文件内容不是审计对象，改动是）。
	Args string `json:"args,omitempty"`
	// Decision 是权限决策标签（spec 规则 102）：rule:<id> / capability
	// / ask:granted(-always) / ask:denied / deny:<id> / ask:no-channel /
	// bypass；空 = 无权限决策路径。
	Decision string `json:"decision,omitempty"`
	IsError  bool   `json:"isError,omitempty"`
	Duration int64  `json:"durationMs"`
}

// argCap 是参数摘要上限（命令可能极长——审计要可读不要倾倒）。
const argCap = 2 << 10

// Record 追加一条（打码 + 截断在写入前统一做）。
func (l *Log) Record(e Entry) {
	if l == nil {
		return // 未开启（nil 面）——调用点零判空
	}
	if len([]rune(e.Args)) > argCap {
		e.Args = string([]rune(e.Args)[:argCap]) + "…"
	}
	e.Args = redact.String(e.Args)
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(l.file(e.Time), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func (l *Log) file(t time.Time) string {
	return filepath.Join(l.dir, "audit-"+t.Format("20060102")+".jsonl")
}

// DayEntry 是按日汇总（CLI 面）。
type DayEntry struct {
	Day     string
	Records int
}

// History 重放近 N 日的审计文件（缺日零条；新到旧）。
func History(dataDir string, days int) []DayEntry {
	if days <= 0 {
		days = 7
	}
	var out []DayEntry
	today := time.Now()
	for i := days - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("20060102")
		raw, err := os.ReadFile(filepath.Join(dataDir, "audit", "audit-"+day+".jsonl"))
		if err != nil {
			out = append(out, DayEntry{Day: day})
			continue
		}
		n := 0
		for _, line := range splitLines(raw) {
			var e Entry
			if json.Unmarshal(line, &e) == nil {
				n++
			}
		}
		out = append(out, DayEntry{Day: day, Records: n})
	}
	return out
}

// splitLines 按行切（兼容 CRLF）。
func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			line := raw[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

// Recent 返回最近 N 条（CLI 面按日回扫）。
func Recent(dataDir string, n int) []Entry {
	if n <= 0 {
		n = 20
	}
	var out []Entry
	today := time.Now()
	for i := 0; i < 30 && len(out) < n; i++ { // 最多回扫 30 天
		day := today.AddDate(0, 0, -i).Format("20060102")
		raw, err := os.ReadFile(filepath.Join(dataDir, "audit", "audit-"+day+".jsonl"))
		if err != nil {
			continue
		}
		lines := splitLines(raw)
		for j := len(lines) - 1; j >= 0 && len(out) < n; j-- {
			var e Entry
			if json.Unmarshal(lines[j], &e) == nil {
				out = append(out, e)
			}
		}
	}
	return out
}
