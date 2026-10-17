package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestLazyLoadSemantics: NewManager reads only meta lines — the shell
// carries the right info without the transcript; the first transcript
// touch loads it; persistence keeps working after a lazy round-trip.
func TestLazyLoadSemantics(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.New("/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "你好，世界"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ai.Message{Role: ai.RoleAssistant,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "收到"}}}); err != nil {
		t.Fatal(err)
	}
	id := s.Info().ID

	// 重启：壳模式。
	m2, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	shell, ok := m2.Get(id)
	if !ok {
		t.Fatal("shell not indexed")
	}
	if shell.loaded {
		t.Fatal("startup parsed the transcript")
	}
	if shell.Info().Workspace != "/tmp/ws" || shell.Info().ID != id {
		t.Fatalf("shell info = %+v", shell.Info())
	}
	// 列表零解析也可用。
	if infos := m2.List(); len(infos) != 1 || infos[0].ID != id {
		t.Fatalf("list = %+v", infos)
	}

	// 首次触及转录：装载。
	msgs := shell.Messages()
	if len(msgs) != 2 || msgs[0].Blocks[0].Text != "你好，世界" {
		t.Fatalf("messages = %+v", msgs)
	}
	if !shell.loaded {
		t.Fatal("not marked loaded")
	}
	// 幂等：再读不重复装载。
	if again := shell.Messages(); len(again) != 2 {
		t.Fatalf("re-read = %d", len(again))
	}

	// 惰性往返后继续追加并再重启：持久化完好。
	if err := shell.Append(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "第三条"}}}); err != nil {
		t.Fatal(err)
	}
	m3, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	s3, ok := m3.Get(id)
	if !ok {
		t.Fatal("lost on second restart")
	}
	if got := len(s3.Messages()); got != 3 {
		t.Fatalf("after lazy append = %d messages", got)
	}
}

// TestLazyLoadBrokenFirstLine: a file whose first line is not meta falls
// back to a full load (loaded shell, transcript intact).
func TestLazyLoadBrokenFirstLine(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	s, err := m.New("/tmp/ws2")
	if err != nil {
		t.Fatal(err)
	}
	s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "x"}}})
	id := s.Info().ID
	path := filepath.Join(dir, id+".jsonl")
	data, _ := os.ReadFile(path)
	// 前置一行垃圾：meta 行不再居首。
	os.WriteFile(path, append([]byte("{\"type\":\"garbage\"}\n"), data...), 0o644)

	m2, _ := NewManager(dir)
	s2, ok := m2.Get(id)
	if !ok {
		t.Fatal("not indexed")
	}
	if !s2.loaded {
		t.Fatal("fallback did not mark loaded")
	}
	if got := s2.Messages(); len(got) != 1 || got[0].Blocks[0].Text != "x" {
		t.Fatalf("messages = %+v", got)
	}
}

// TestNewManagerStartupCost: with N sessions of big transcripts, the
// manager builds from meta lines only — measured against eager full
// parsing of the same directory on the same machine (cache-warm).
func TestNewManagerStartupCost(t *testing.T) {
	dir := t.TempDir()
	const sessions = 40
	const filler = 40 * 1024 // 每条消息 ~40KB 工具输出

	m, _ := NewManager(dir)
	ids := make([]string, 0, sessions)
	for i := 0; i < sessions; i++ {
		s, err := m.New("/tmp/ws")
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 8; j++ {
			s.Append(ai.Message{Role: ai.RoleToolResult, Blocks: []ai.Block{
				{Kind: ai.BlockToolResult, Text: strings.Repeat("x", filler)}}})
		}
		ids = append(ids, s.Info().ID)
	}
	// 预热文件缓存（写入后的首读含系统缓存成本，不比较它）。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jsonl" {
			if f, err := os.Open(filepath.Join(dir, e.Name())); err == nil {
				f.Close()
			}
		}
	}

	// 基准：全量解析同目录。
	eagerStart := time.Now()
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jsonl" {
			loadSessionSafe(filepath.Join(dir, e.Name()))
		}
	}
	eager := time.Since(eagerStart)

	lazyStart := time.Now()
	m2, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	lazy := time.Since(lazyStart)

	for _, id := range ids {
		s, ok := m2.Get(id)
		if !ok || s.loaded {
			t.Fatalf("session %s loaded=%v ok=%v", id, s != nil && s.loaded, ok)
		}
	}
	t.Logf("eager=%v lazy=%v", eager, lazy)
	if lazy > eager/4 {
		t.Fatalf("lazy startup %v not meaningfully faster than eager %v", lazy, eager)
	}
}
