package sessionidx

// 索引核心行为测试：重放态抽取语义、增量保鲜（reads 观测）、落盘
// 往返与自愈、超限退化。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func msg(ord int, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "message",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	})
	return string(b)
}

func regen(kept int) string {
	b, _ := json.Marshal(map[string]any{"type": "regenerate", "kept": kept})
	return string(b)
}

func compact(kept int, summary string) string {
	b, _ := json.Marshal(map[string]any{"type": "compaction", "kept": kept, "summary": summary})
	return string(b)
}

// TestExtractReplaySemantics：regenerate 截断、compaction 换摘要
// （负序号）、坏行跳过——只留最终存活段，文件序。
func TestExtractReplaySemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		"not json at all",         // 坏行：跳过
		msg(0, "alpha keep"),      // 存活
		msg(1, "beta dropped"),    // regenerate 掐掉
		regen(1),                  // 保留前 1 条
		msg(2, "gamma keep"),      // 存活
		compact(1, "summary syn"), // 前缀换摘要：alive=[syn, gamma]
		msg(3, "delta keep"),      // 存活
	)
	segs, over, err := Extract(path)
	if err != nil || over {
		t.Fatalf("extract: %v over=%v", err, over)
	}
	var got []string
	for _, s := range segs {
		got = append(got, s.Text)
	}
	// 文件序（只列存活者）：gamma、summary（合成，出现在 compaction
	// 行位置）、delta。beta 被 regenerate 掐掉；alpha 被 compaction
	// 换成摘要（kept=1 吃掉前缀）。
	want := []string{"gamma keep", "summary syn", "delta keep"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("segs = %v, want %v", got, want)
	}
}

// TestExtractToolArgsSegments：工具参数段在文本段之后、标记 Args。
func TestExtractToolArgsSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	b, _ := json.Marshal(map[string]any{
		"type": "message",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "Doing it"},
				{"type": "toolCall", "name": "bash", "arguments": `{"cmd":"ls -la"}`},
			},
		},
	})
	writeLines(t, path, string(b))
	segs, _, err := Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || segs[0].Args || !strings.Contains(segs[0].Text, "doing it") {
		t.Fatalf("text seg = %+v", segs[0])
	}
	if !segs[1].Args || !strings.Contains(segs[1].Text, "ls -la") {
		t.Fatalf("args seg = %+v", segs[1])
	}
}

// TestRefreshIncremental：mtime/size 命中不重读；变更/收缩/删除重判。
func TestRefreshIncremental(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeLines(t, a, msg(0, "hello alpha"))
	writeLines(t, b, msg(0, "hello beta"))
	idx := Load(filepath.Join(t.TempDir(), "idx.json"))
	if idx.Refresh([]string{"a", "b"}, dir) != true {
		t.Fatal("first refresh should change")
	}
	if idx.Reads() != 2 {
		t.Fatalf("reads = %d", idx.Reads())
	}
	// 无变更：零新读。
	if idx.Refresh([]string{"a", "b"}, dir) {
		t.Fatal("unchanged refresh reported change")
	}
	if idx.Reads() != 2 {
		t.Fatalf("reads after no-op = %d", idx.Reads())
	}
	// 追加（size 变）：该会话重读。
	writeLines(t, a, msg(0, "hello alpha"), msg(1, "second"))
	if !idx.Refresh([]string{"a", "b"}, dir) {
		t.Fatal("append refresh should change")
	}
	if idx.Reads() != 3 {
		t.Fatalf("reads after append = %d", idx.Reads())
	}
	// 删除：条目剪除。
	os.Remove(b)
	if !idx.Refresh([]string{"a"}, dir) {
		t.Fatal("delete refresh should change")
	}
	if idx.EntryCount() != 1 || idx.Lookup("b") != nil {
		t.Fatal("b not pruned")
	}
	// Lookup 走索引：命中条目。
	if e := idx.Lookup("a"); e == nil || len(e.Segments) != 2 {
		t.Fatalf("lookup a = %+v", e)
	}
}

// TestSaveLoadRoundtripAndHeal：落盘往返；损坏/版本不符自愈为空。
func TestSaveLoadRoundtripAndHeal(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	writeLines(t, a, msg(0, "persisted text"))
	ipath := filepath.Join(dir, "idx.json")
	idx := Load(ipath)
	idx.Refresh([]string{"a"}, dir)
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	again := Load(ipath)
	if again.EntryCount() != 1 {
		t.Fatalf("roundtrip entries = %d", again.EntryCount())
	}
	// 不重读（保鲜命中）。
	again.Refresh([]string{"a"}, dir)
	if again.Reads() != 0 {
		t.Fatalf("reads after reload = %d (want 0)", again.Reads())
	}
	// 损坏 → 空。
	os.WriteFile(ipath, []byte("{not json"), 0o644)
	if Load(ipath).EntryCount() != 0 {
		t.Fatal("corrupt snapshot not discarded")
	}
	// 版本不符 → 空。
	blob, _ := json.Marshal(map[string]any{"version": 999, "entries": []any{}})
	os.WriteFile(ipath, blob, 0o644)
	if Load(ipath).EntryCount() != 0 {
		t.Fatal("future version not discarded")
	}
}

// TestOversize：单会话 corpus 超限 → Oversize、段弃、Lookup 退 nil。
func TestOversize(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.jsonl")
	var lines []string
	for i := 0; i < 40; i++ { // 40 × 30KB > 1MB
		lines = append(lines, msg(i, strings.Repeat("x", 30_000)))
	}
	writeLines(t, big, lines...)
	idx := Load(filepath.Join(t.TempDir(), "idx.json"))
	idx.Refresh([]string{"big"}, dir)
	if idx.Lookup("big") != nil {
		t.Fatal("oversize session should not be indexed")
	}
	// 但条目存在（记 size/mtime，避免下次重判）。
	if idx.EntryCount() != 1 {
		t.Fatalf("entries = %d", idx.EntryCount())
	}
	// 无变更再刷新：不重读（Oversize 也保鲜）。
	idx.Refresh([]string{"big"}, dir)
	if idx.Reads() != 1 {
		t.Fatalf("reads = %d (oversize should stay fresh)", idx.Reads())
	}
}

// TestMtimeGranularity：同尺寸内容变更靠 mtime 兜底（nano 粒度下
// 连写两次同尺寸也该被识破——UnixNano 含纳秒）。
func TestMtimeGranularity(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	writeLines(t, a, msg(0, "first text"))
	idx := Load(filepath.Join(t.TempDir(), "i.json"))
	idx.Refresh([]string{"a"}, dir)
	time.Sleep(5 * time.Millisecond)          // 文件系统 mtime 粒度余量
	writeLines(t, a, msg(0, "same len text")) // 等长内容变更
	if !idx.Refresh([]string{"a"}, dir) {
		t.Fatal("equal-size rewrite not detected")
	}
	if e := idx.Lookup("a"); e == nil || !strings.Contains(e.Segments[0].Text, "same len") {
		t.Fatalf("stale segment: %+v", e)
	}
}
