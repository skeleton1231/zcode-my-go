package audit

// 审计日志测试（spec 规则 100）：落盘形态、打码、截断、按日汇总、
// 最近条回扫。假密钥低熵重复串。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordAndRedaction(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	key := "sk-ant-" + strings.Repeat("a", 24)
	l.Record(Entry{Time: time.Now(), Tool: "bash", Args: "curl -H \"Auth: " + key + "\" example.com", Duration: 12})
	l.Record(Entry{Time: time.Now(), Tool: "write", Args: "notes.txt", IsError: true})
	l.Record(Entry{Time: time.Now(), Tool: "bash", Args: strings.Repeat("x", argCap+500)}) // 超长截断

	path := filepath.Join(dir, "audit", "audit-"+time.Now().Format("20060102")+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatalf("secret leaked:\n%s", raw)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	var e Entry
	json.Unmarshal([]byte(lines[1]), &e)
	if e.Tool != "write" || !e.IsError || e.Args != "notes.txt" {
		t.Fatalf("entry = %+v", e)
	}
	var long Entry
	json.Unmarshal([]byte(lines[2]), &long)
	if len([]rune(long.Args)) > argCap+2 { // 截断 + 省略号
		t.Fatalf("args not capped: %d", len([]rune(long.Args)))
	}
	// nil Log 安全空转。
	var nilLog *Log
	nilLog.Record(Entry{Tool: "bash"})
}

func TestHistoryAndRecent(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("20060102")
	os.MkdirAll(filepath.Join(dir, "audit"), 0o700)
	os.WriteFile(filepath.Join(dir, "audit", "audit-"+today+".jsonl"),
		[]byte(`{"tool":"bash","durationMs":5}`+"\n"+`{"tool":"read"}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "audit", "audit-"+time.Now().AddDate(0, 0, -3).Format("20060102")+".jsonl"),
		[]byte(`{"tool":"edit"}`+"\n"), 0o600)

	hist := History(dir, 5)
	if len(hist) != 5 {
		t.Fatalf("hist days = %d", len(hist))
	}
	var todayN, oldN, missing int
	for _, d := range hist {
		switch {
		case d.Day == today:
			todayN = d.Records
		case d.Records == 1:
			oldN++
		case d.Records == 0:
			missing++
		}
	}
	if todayN != 2 || oldN != 1 || missing != 3 {
		t.Fatalf("hist = %+v", hist)
	}
	recent := Recent(dir, 2)
	if len(recent) != 2 || recent[0].Tool != "read" { // 新到旧：read 是最后一条
		t.Fatalf("recent = %+v", recent)
	}
}

// TestSearchFilters（spec 规则 106）：工具/决策/时限/仅错误过滤与
// limit——组合命中与互斥排除。
func TestSearchFilters(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "audit"), 0o700)
	entries := []Entry{
		{Time: time.Now().Add(-1 * time.Hour), Tool: "bash", Args: "git status", Decision: "capability"},
		{Time: time.Now().Add(-2 * time.Hour), Tool: "bash", Args: "rm -rf x", Decision: "ask:denied", IsError: true},
		{Time: time.Now().Add(-3 * time.Hour), Tool: "write", Args: "a.txt", Decision: "ask:granted"},
		{Time: time.Now().Add(-200 * time.Hour), Tool: "bash", Args: "old command", Decision: "capability"},
	}
	var b []byte
	for _, e := range entries[:3] { // 今天文件只放 24h 内的三条
		line, _ := json.Marshal(e)
		b = append(b, line...)
		b = append(b, '\n')
	}
	os.WriteFile(filepath.Join(dir, "audit", "audit-"+time.Now().Format("20060102")+".jsonl"), b, 0o600)
	// 200h 前的条目单独放昨天的文件（Since 过滤要跨文件仍被时限排除）。
	oldLine, _ := json.Marshal(entries[3])
	yesterday := time.Now().AddDate(0, 0, -1)
	os.WriteFile(filepath.Join(dir, "audit", "audit-"+yesterday.Format("20060102")+".jsonl"), oldLine, 0o600)

	if got := Search(dir, Query{Tool: "bash"}); len(got) != 3 {
		t.Fatalf("tool filter = %d", len(got))
	}
	if got := Search(dir, Query{Decision: "ask:denied"}); len(got) != 1 || got[0].Tool != "bash" {
		t.Fatalf("decision filter = %+v", got)
	}
	if got := Search(dir, Query{Since: 24 * time.Hour}); len(got) != 3 {
		t.Fatalf("since filter = %d（200h 前的两条都排除）", len(got))
	}
	if got := Search(dir, Query{OnlyErr: true}); len(got) != 1 {
		t.Fatalf("onlyErr = %d", len(got))
	}
	if got := Search(dir, Query{Limit: 2}); len(got) != 2 {
		t.Fatalf("limit = %d", len(got))
	}
	// 组合。
	if got := Search(dir, Query{Tool: "bash", Since: 24 * time.Hour}); len(got) != 2 {
		t.Fatalf("combo = %d", len(got))
	}
	// 默认 limit 50 内零命中。
	if got := Search(dir, Query{Tool: "nope"}); len(got) != 0 {
		t.Fatalf("no hit = %d", len(got))
	}
}
