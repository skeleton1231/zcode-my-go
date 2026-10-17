package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent/compact"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// summaryProvider answers the summary request with fixed text.
type summaryProvider struct{ reply string }

func (p summaryProvider) Stream(_ context.Context, _ ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: p.reply}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// TestCompactReplacesHistoryAndPersists verifies the manual compaction:
// summary message plus tail, replayed identically from the JSONL record.
func TestCompactReplacesHistoryAndPersists(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	m.Wire(Wiring{
		Provider: func() ai.Provider { return summaryProvider{reply: "这是摘要"} },
		Model:    func() string { return "m" },
	})
	s, _ := m.New("ws")
	for i := 0; i < 10; i++ {
		role := ai.RoleUser
		if i%2 == 1 {
			role = ai.RoleAssistant
		}
		if err := s.Append(ai.Message{Role: role, Blocks: []ai.Block{
			{Kind: ai.BlockText, Text: "old"}}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	got := s.Messages()
	if len(got) != compact.DefaultKeepTail+1 {
		t.Fatalf("messages after compact = %d, want %d", len(got), compact.DefaultKeepTail+1)
	}
	if !strings.Contains(got[0].Text(), "这是摘要") {
		t.Fatalf("summary message = %q", got[0].Text())
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, ok := reloaded.Get(s.Info().ID)
	if !ok {
		t.Fatal("session lost")
	}
	if len(again.Messages()) != len(got) {
		t.Fatalf("replayed = %d, want %d", len(again.Messages()), len(got))
	}
	if !strings.Contains(again.Messages()[0].Text(), "这是摘要") {
		t.Fatalf("replayed summary = %q", again.Messages()[0].Text())
	}
}

// TestCompactFailureCircuitBreaker verifies failed summaries count toward
// the breaker and auto-compact gives up after the limit.
func TestCompactFailureCircuitBreaker(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	// Provider whose summary calls fail: reply to nothing with an error end.
	fail := failProvider{msg: "summary failed"}
	m.Wire(Wiring{
		Provider: func() ai.Provider { return fail },
		Model:    func() string { return "m" },
	})
	s, _ := m.New("ws")
	for i := 0; i < 4; i++ {
		if err := s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{
			{Kind: ai.BlockText, Text: strings.Repeat("字", 170000)}}}); err != nil {
			t.Fatal(err)
		}
	}

	// 三次失败进入熔断；压缩不阻塞 Chat，历史保持原样。
	for i := 0; i < 4; i++ {
		var events []Event
		_ = s.Chat("继续", func(ev Event) { events = append(events, ev) })
	}
	s.mu.Lock()
	failures := s.compactFailures
	s.mu.Unlock()
	if failures != 3 {
		t.Fatalf("failures = %d, want breaker at 3", failures)
	}
	if len(s.Messages()) == 0 {
		t.Fatal("history lost")
	}
}
