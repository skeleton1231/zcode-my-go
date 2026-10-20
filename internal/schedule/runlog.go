package schedule

// 运行历史（spec 规则 118）：任务的每次 fire/手动触发追加一条记录到
// <dir>/job-logs/<id>.jsonl（时间/状态/摘要/来源 tick|manual）；上限
// 200 条（环形裁剪：重写保留尾部）。zgo cron log <id> 查询。与
// jobs.json 的 LastStatus/LastSummary（最新一次）互补——历史可答「这
// 个任务最近一周都跑成什么样」。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// runLogMax 是单任务历史条数上限（环形裁剪）。
const runLogMax = 200

// RunEntry 是一次任务运行的历史记录。
type RunEntry struct {
	Time    time.Time `json:"time"`
	Status  string    `json:"status"`
	Summary string    `json:"summary,omitempty"`
	Source  string    `json:"source,omitempty"` // tick | manual
}

// runLogCache 进程内单飞写（同任务并发 fire 的追加串行化）。
var runLogMu sync.Mutex

// appendRun 追加一条运行历史（原子追加；失败静默——历史是尽力而为
// 的观测面，写失败不该影响任务本身）。
func appendRun(dir, id string, e RunEntry) {
	runLogMu.Lock()
	defer runLogMu.Unlock()
	d := filepath.Join(dir, "job-logs")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	path := filepath.Join(d, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return
	}
	// Windows 文件共享：句柄未关时 ReadFile 撞共享违例——先关再裁。
	f.Close()
	// 超限裁剪（读全量→留尾部→重写）：任务低频，全量重写成本可忽略。
	trimRunLog(path)
}

// trimRunLog 超过 runLogMax 条时保留尾部（原子重写）。
func trimRunLog(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := splitRunLines(raw)
	if len(lines) <= runLogMax {
		return
	}
	var b []byte
	for _, l := range lines[len(lines)-runLogMax:] {
		b = append(b, l...)
		b = append(b, '\n')
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	os.Rename(tmp, path)
}

// RunHistory 读任务历史（新到旧；limit<=0 默认 20）。任务不存在或无
// 历史返回空。
func RunHistory(dir, id string, limit int) []RunEntry {
	if limit <= 0 {
		limit = 20
	}
	raw, err := os.ReadFile(filepath.Join(dir, "job-logs", id+".jsonl"))
	if err != nil {
		return nil
	}
	lines := splitRunLines(raw)
	var out []RunEntry
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		var e RunEntry
		if json.Unmarshal(lines[i], &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// splitRunLines 按行切（CRLF 兼容）。
func splitRunLines(raw []byte) [][]byte {
	var out [][]byte
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		l = strings.TrimRight(l, "\r")
		if l != "" {
			out = append(out, []byte(l))
		}
	}
	return out
}
