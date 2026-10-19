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
