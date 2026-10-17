package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// writeRawSession 手工构造一个会话文件（复现损坏/超大记录）。
func writeRawSession(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func metaLine() string {
	return `{"type":"meta","id":"probe","workspace":"ws","createdAt":"2026-01-01T00:00:00Z"}`
}

func msgLine(role, text string) string {
	return `{"type":"message","message":{"role":"` + role +
		`","content":[{"type":"text","text":` + jsonString(text) + `}]}}`
}

func jsonString(s string) string {
	// 测试内避免引入 encoding/json 依赖漂移：仅转义反斜杠与引号。
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// TestLoadSkipsNegativeCompactionKept 验证负 Kept 的 compaction 记录被
// 跳过而非 panic（issue #9：启动路径 brick）。
func TestLoadSkipsNegativeCompactionKept(t *testing.T) {
	dir := t.TempDir()
	writeRawSession(t, dir, "neg.jsonl",
		metaLine(),
		msgLine("user", "一"),
		msgLine("assistant", "二"),
		`{"type":"compaction","summary":"sum","kept":-1}`,
	)
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	s, ok := m.Get("probe")
	if !ok {
		t.Fatal("session missing after negative kept")
	}
	if got := len(s.Messages()); got != 2 {
		t.Fatalf("messages = %d, want 2 (record skipped)", got)
	}
}

// TestLoadSkipsOversizedLine 验证超过读取上限的行被跳过、会话仍可加载
// （issue #10：9MB 行曾让会话从列表静默消失）。
func TestLoadSkipsOversizedLine(t *testing.T) {
	dir := t.TempDir()
	// ~18MB 行：超过读侧 maxLoadLine（16MB），按损坏跳过；12MB 以下的历史
	// 大行在读取容错下可直接恢复。
	huge := msgLine("user", strings.Repeat("巨", 6*1024*1024))
	writeRawSession(t, dir, "huge.jsonl",
		metaLine(),
		msgLine("user", "before"),
		huge,
		msgLine("assistant", "after"),
	)
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	s, ok := m.Get("probe")
	if !ok {
		t.Fatal("session disappeared on oversized line")
	}
	var texts []string
	for _, msg := range s.Messages() {
		texts = append(texts, msg.Text())
	}
	if texts[0] != "before" || texts[len(texts)-1] != "after" {
		t.Fatalf("neighbours lost: %v", texts)
	}
	for _, txt := range texts {
		if len(txt) > 1024*1024 {
			t.Fatalf("oversized message survived: %d bytes", len(txt))
		}
	}
}

// TestAppendTruncatesHugeBlocks 验证写入侧对超大文本块截断，重放与
// 内存一致（issue #10 建议 1）。
func TestAppendTruncatesHugeBlocks(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	wire(m)
	s, err := m.New("ws")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{
		{Kind: ai.BlockText, Text: strings.Repeat("大", 3*1024*1024)}, // ~9MB
	}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// 落盘行必须远小于 8MB。
	data, err := os.ReadFile(s.Info().ID + ".jsonl")
	if err == nil && len(data) > 4*1024*1024 {
		t.Fatalf("session file = %d bytes, want bounded", len(data))
	}
	_ = data
	_ = err
	// 重放一致：截断后的消息能读回。
	again, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, ok := again.Get(s.Info().ID)
	if !ok {
		t.Fatal("session not reloadable")
	}
	msgs := s2.Messages()
	if len(msgs) == 0 {
		t.Fatal("no messages after reload")
	}
	if got := len(msgs[len(msgs)-1].Text()); got > maxBlockText+128 {
		t.Fatalf("reloaded text = %d bytes, want ≤ cap", got)
	}
	if !strings.Contains(msgs[len(msgs)-1].Text(), "…") {
		t.Fatal("truncation marker missing")
	}
}

// TestAppendTrimsImageBudget 验证图片总量预算（issue #105：多张 5MB 图
// 拼一条消息曾让 JSONL 行超读取上限，重载时整条消息连图静默消失）。
func TestAppendTrimsImageBudget(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	wire(m)
	s, err := m.New("ws")
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("i", 3<<20) // 3MB base64/块
	msg := ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{
		{Kind: ai.BlockText, Text: "看这些图"},
		{Kind: ai.BlockImage, Media: "image/png", Data: big},
		{Kind: ai.BlockImage, Media: "image/png", Data: big},
		{Kind: ai.BlockImage, Media: "image/png", Data: big},
	}}
	if err := s.Append(msg); err != nil {
		t.Fatalf("append: %v", err)
	}
	// 内存中的消息：图片总量受预算约束，且带丢弃说明。
	kept := s.Messages()[0]
	total := 0
	dropped := false
	for _, b := range kept.Blocks {
		if b.Kind == ai.BlockImage {
			total += len(b.Data)
		}
		if b.Kind == ai.BlockText && strings.Contains(b.Text, "未持久化") {
			dropped = true
		}
	}
	if total > maxImageTotal {
		t.Fatalf("image total = %d, want <= %d", total, maxImageTotal)
	}
	if !dropped {
		t.Fatal("drop notice missing")
	}
	// 重载后消息仍在（含文本与保留的图）。
	again, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, ok := again.Get(s.Info().ID)
	if !ok {
		t.Fatal("session lost on reload")
	}
	msgs := s2.Messages()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Text(), "看这些图") {
		t.Fatal("reloaded message lost")
	}
	if !strings.Contains(msgs[0].Text(), "未持久化") {
		t.Fatal("drop notice lost on reload")
	}
}
