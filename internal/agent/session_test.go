package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// wire installs minimal fake dependencies.
func wire(m *Manager) {
	m.Wire(Wiring{
		Provider: func() ai.Provider { return failProvider{msg: "no provider in tests"} },
		Model:    func() string { return "fake-model" },
		System:   func(string) string { return "system prompt" },
	})
}

// TestSessionPersistsAcrossReload verifies the JSONL round trip.
func TestSessionPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	wire(m)
	s, err := m.New(`D:\ws`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "fix the bug in main.go please"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ai.Message{Role: ai.RoleAssistant,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "on it"}}}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(s.Info().ID)
	if !ok {
		t.Fatal("session lost after reload")
	}
	if len(got.Messages()) != 2 || got.Messages()[1].Text() != "on it" {
		t.Fatalf("messages = %+v", got.Messages())
	}
	info := got.Info()
	if info.Title != "fix the bug in main.go please" {
		t.Fatalf("title = %q", info.Title)
	}
	if info.Workspace != `D:\ws` {
		t.Fatalf("workspace = %q", info.Workspace)
	}
}

// TestSessionTitleIsSummarized verifies long first messages are shortened.
func TestSessionTitleIsSummarized(t *testing.T) {
	s := &Session{}
	long := strings.Repeat("word ", 30)
	s.Append(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: long}}})
	if title := s.Info().Title; len([]rune(title)) != 40 {
		t.Fatalf("title len = %d: %q", len([]rune(title)), title)
	}
}

// TestChatGuardsDoubleRun verifies the running flag.
func TestChatGuardsDoubleRun(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	wire(m)
	s, _ := m.New("ws")

	blocking := make(chan struct{})
	m.Wire(Wiring{
		Provider: func() ai.Provider { return blockProvider{until: blocking} },
		Model:    func() string { return "m" },
		System:   func(string) string { return "s" },
	})

	done := make(chan error, 1)
	go func() { done <- s.Chat("hello", func(Event) {}) }()
	waitForRunning(t, s)

	if err := s.Chat("again", func(Event) {}); err == nil ||
		!strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("double run allowed: %v", err)
	}
	s.Abort()
	close(blocking)
	if err := <-done; err != nil {
		t.Fatalf("first run failed: %v", err)
	}
}

// TestAbortEndsRun verifies abort cancels the provider stream and the
// session becomes reusable.
func TestAbortEndsRun(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	wire(m)
	s, _ := m.New("ws")

	blocking := make(chan struct{})
	m.Wire(Wiring{
		Provider: func() ai.Provider { return blockProvider{until: blocking} },
		Model:    func() string { return "m" },
		System:   func(string) string { return "s" },
	})

	var events []Event
	done := make(chan error, 1)
	go func() { done <- s.Chat("hello", func(ev Event) { events = append(events, ev) }) }()
	waitForRunning(t, s)
	s.Abort()
	close(blocking) // abort releases the provider's blocked stream
	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == EvAgentEnd {
			found = true
		}
	}
	if !found {
		t.Fatalf("no agent_end: %+v", events)
	}
	if s.Info().Running {
		t.Fatal("session still running after abort")
	}
}

// blockProvider's stream stays open until its channel closes, modeling a
// stalled provider.
type blockProvider struct{ until chan struct{} }

func (p blockProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 1)
	go func() {
		defer close(out)
		select {
		case <-p.until:
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		case <-ctx.Done():
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopAborted}
		}
	}()
	return out
}

// waitForRunning polls until the session reports itself running.
func waitForRunning(t *testing.T, s *Session) {
	t.Helper()
	for range 100 {
		if s.Info().Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session never started running")
}

// TestManagerListOrdersNewestFirst verifies index ordering.
func TestManagerListOrdersNewestFirst(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	wire(m)
	first, _ := m.New("ws1")
	time.Sleep(2 * time.Millisecond) // ids are timestamp-derived
	second, _ := m.New("ws2")

	list := m.List()
	if len(list) != 2 || list[0].ID != second.Info().ID || list[1].ID != first.Info().ID {
		t.Fatalf("list = %+v", list)
	}
	if err := m.Delete(first.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get(first.Info().ID); ok {
		t.Fatal("deleted session still indexed")
	}
}
