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

// TestRetryRegenerates verifies retry truncates back to the last user
// message, re-runs, and replays identically from the JSONL record.
func TestRetryRegenerates(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	m.Wire(Wiring{
		Provider: func() ai.Provider { return failProvider{msg: "no provider"} },
		Model:    func() string { return "fake-model" },
	})
	s, _ := m.New("ws")
	s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "first"}}})
	s.Append(ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "bad answer"}}})
	s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "second"}}})
	s.Append(ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "worse"}}})

	if err := s.Retry(func(Event) {}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// 截断到第二条 user 之前，重跑后历史以 user "second" + 新回合收尾。
	msgs := s.Messages()
	if len(msgs) < 3 || msgs[len(msgs)-2].Text() != "second" {
		t.Fatalf("messages after retry = %+v", msgs)
	}
	if msgs[0].Text() != "first" || msgs[1].Text() != "bad answer" {
		t.Fatalf("earlier turns lost: %+v", msgs[:2])
	}

	reloaded, _ := NewManager(dir)
	again, ok := reloaded.Get(s.Info().ID)
	if !ok {
		t.Fatal("session lost")
	}
	rmsgs := again.Messages()
	if len(rmsgs) != len(msgs) {
		t.Fatalf("replayed = %d, want %d", len(rmsgs), len(msgs))
	}
}

func TestRetryGuards(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	wire(m)
	s, _ := m.New("ws")
	if err := s.Retry(func(Event) {}); err == nil {
		t.Fatal("empty retry allowed")
	}
}

// TestEditAndResendReplacesLastUserMessage verifies the edit path truncates
// back to (not including) the last user message and runs with new text.
func TestEditAndResendReplacesLastUserMessage(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	m.Wire(Wiring{
		Provider: func() ai.Provider { return failProvider{msg: "no provider"} },
		Model:    func() string { return "fake-model" },
	})
	s, _ := m.New("ws")
	s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "keep me"}}})
	s.Append(ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "old answer"}}})
	s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "fix this"}}})

	if err := s.EditAndResend("改成那样", func(Event) {}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	msgs := s.Messages()
	if msgs[0].Text() != "keep me" || msgs[1].Text() != "old answer" {
		t.Fatalf("earlier turns lost: %+v", msgs[:2])
	}
	found := false
	for _, m := range msgs[2:] {
		if m.Role == ai.RoleUser && m.Text() == "改成那样" {
			found = true
		}
	}
	if !found {
		t.Fatalf("edited prompt missing: %+v", msgs[2:])
	}
	for _, m := range msgs {
		if m.Role == ai.RoleUser && m.Text() == "fix this" {
			t.Fatal("old prompt survived the edit")
		}
	}

	// 重放一致性。
	reloaded, _ := NewManager(dir)
	if got := len(reloaded.List()); got != 1 {
		t.Fatalf("sessions = %d", got)
	}
	again, _ := reloaded.Get(s.Info().ID)
	if len(again.Messages()) != len(msgs) {
		t.Fatalf("replayed = %d, want %d", len(again.Messages()), len(msgs))
	}
}

func TestEditAndResendGuards(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	wire(m)
	s, _ := m.New("ws")
	if err := s.EditAndResend("hi", func(Event) {}); err == nil {
		t.Fatal("edit without history allowed")
	}
	s.Append(ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "x"}}})
	if err := s.EditAndResend("  ", func(Event) {}); err == nil {
		t.Fatal("empty edit allowed")
	}
}

// TestForkAtUserTurn verifies a fork copies the prefix before the nth
// user message, leaves the source intact, and persists for reload.
func TestForkAtUserTurn(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	wire(m)
	src, _ := m.New("ws")
	src.Append(userMsg("第一问"))
	src.Append(ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "答一"}}})
	src.Append(userMsg("第二问"))
	src.Append(ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "答二"}}})
	src.Append(userMsg("第三问"))

	fork, err := m.ForkAtUserTurn(src, 2)
	if err != nil {
		t.Fatal(err)
	}
	msgs := fork.Messages()
	if len(msgs) != 2 || msgs[0].Text() != "第一问" || msgs[1].Text() != "答一" {
		t.Fatalf("fork messages = %+v", msgs)
	}
	if fork.Info().Workspace != "ws" || fork.Info().Title == "" {
		t.Fatalf("fork info = %+v", fork.Info())
	}
	// 源会话原样。
	if len(src.Messages()) != 5 {
		t.Fatalf("source mutated: %d", len(src.Messages()))
	}
	// 越界拒绝。
	if _, err := m.ForkAtUserTurn(src, 9); err == nil {
		t.Fatal("out-of-range turn accepted")
	}
	// 分叉可重载。
	reloaded, _ := NewManager(dir)
	again, ok := reloaded.Get(fork.Info().ID)
	if !ok || len(again.Messages()) != 2 {
		t.Fatalf("fork reload = %v %d", ok, len(again.Messages()))
	}
}

func userMsg(text string) ai.Message {
	return ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}}
}

// TestUsageTotalAccumulatesAndPersists verifies per-run accumulation and
// the usage record's replay on load.
func TestUsageTotalAccumulatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	m.Wire(Wiring{
		Provider: func() ai.Provider { return failProvider{msg: "x"} },
		Model:    func() string { return "m" },
	})
	s, _ := m.New("ws")

	// 模拟两次运行落账。
	s.addUsage(ai.Usage{InputTokens: 100, OutputTokens: 20})
	s.addUsage(ai.Usage{InputTokens: 50, OutputTokens: 10})
	total := s.UsageTotal()
	if total.InputTokens != 150 || total.OutputTokens != 30 {
		t.Fatalf("total = %+v", total)
	}

	reloaded, _ := NewManager(dir)
	again, _ := reloaded.Get(s.Info().ID)
	if got := again.UsageTotal(); got.InputTokens != 150 || got.OutputTokens != 30 {
		t.Fatalf("reloaded total = %+v", got)
	}
}
