package agent

// 存量清洗测试：三类自由文本载体（消息块/压缩摘要/meta 标题）命中、
// 其余行字节不动、dry-run 不落盘、清洗后转录仍可装载回放。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/redact"
)

func mk(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "s.jsonl")
}

// fixture 是一份含密钥各处的转录（消息文本、工具参数、摘要、标题）
// 加两行必须原样的行（usage / 未知类型）。
func fixture(t *testing.T) string {
	t.Helper()
	t.Cleanup(func() { redact.Set() })
	p := mk(t)
	own := "legacy-exact-key-9876543210fedcba"
	redact.Set(own)
	lines := []string{
		`{"type":"meta","title":"讨论 ` + "sk-legacy-abcdefghijklmnopqrst" + `","workspace":"D:/x","createdAt":"2026-10-01T00:00:00Z"}`,
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"MY_KEY=` + own + ` tail"}]}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash","arguments":"{\"cmd\":\"echo sk-ant-legacy-abcdefghijklmnopqrs\"}"}]}}`,
		`{"type":"compaction","kept":2,"summary":"旧摘要里有 sk-legacy-abcdefghijklmnopqrst"}`,
		`{"type":"usage","usage":{"input":10,"output":5}}`,
		`{"type":"weird-future","payload":{"x":1}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSanitizeDryRunDoesNotWrite(t *testing.T) {
	p := fixture(t)
	before, _ := os.ReadFile(p)
	res, err := SanitizeSessionFile(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Redacted != 4 { // meta + 两条消息 + 摘要
		t.Fatalf("redacted = %d (want 4): %+v", res.Redacted, res)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("dry-run modified the file")
	}
}

func TestSanitizeApplyCleansAndPreserves(t *testing.T) {
	p := fixture(t)
	if _, err := SanitizeSessionFile(p, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	text := string(raw)
	for _, leak := range []string{"legacy-exact-key", "sk-legacy-abcdefghijklmnopqrst", "sk-ant-legacy-abcdefghijklmnopqrs"} {
		if strings.Contains(text, leak) {
			t.Fatalf("raw secret survived: %s", leak)
		}
	}
	if !strings.Contains(text, "[REDACTED:secret]") || !strings.Contains(text, "[REDACTED:anthropic-key]") ||
		!strings.Contains(text, "[REDACTED:openai-key]") {
		t.Fatalf("redaction markers missing:\n%s", text)
	}
	// 未命中行字节不动：usage 与未知类型行原样。
	if !strings.Contains(text, `"type":"usage","usage":{"input":10,"output":5}`) {
		t.Fatalf("usage line altered:\n%s", text)
	}
	if !strings.Contains(text, `"type":"weird-future","payload":{"x":1}`) {
		t.Fatalf("unknown-type line altered:\n%s", text)
	}
	// role 透传（map 手术不丢字段）。
	if !strings.Contains(text, `"role":"user"`) || !strings.Contains(text, `"role":"assistant"`) {
		t.Fatalf("role dropped:\n%s", text)
	}
	// kept 透传。
	if !strings.Contains(text, `"kept":2`) {
		t.Fatalf("kept dropped:\n%s", text)
	}
	// 每行仍是合法 JSON。
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		var v any
		if json.Unmarshal([]byte(line), &v) != nil {
			t.Fatalf("line %d not JSON: %s", i, line)
		}
	}
}

func TestSanitizeIdempotent(t *testing.T) {
	p := fixture(t)
	if _, err := SanitizeSessionFile(p, false); err != nil {
		t.Fatal(err)
	}
	res, err := SanitizeSessionFile(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Redacted != 0 {
		t.Fatalf("second pass rewrote %d lines (want 0)", res.Redacted)
	}
}

func TestSanitizeDir(t *testing.T) {
	t.Cleanup(func() { redact.Set() })
	dir := t.TempDir()
	redact.Set("dirlevel-secret-0123456789")
	os.WriteFile(filepath.Join(dir, "a.jsonl"),
		[]byte(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"k=dirlevel-secret-0123456789"}]}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.jsonl"),
		[]byte(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"clean"}]}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "note.txt"), []byte("not a session\n"), 0o644)
	per, total, err := SanitizeDir(dir, true)
	if err != nil || total != 1 || per["a.jsonl"].Redacted != 1 || per["b.jsonl"].Redacted != 0 {
		t.Fatalf("dir dry-run: total=%d per=%+v err=%v", total, per, err)
	}
	if _, ok := per["note.txt"]; ok {
		t.Fatal("non-jsonl file scanned")
	}
	// 缺目录：空结果无错。
	if _, total, err := SanitizeDir(filepath.Join(dir, "missing"), true); err != nil || total != 0 {
		t.Fatalf("missing dir: %v", err)
	}
}
