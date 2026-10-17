// Package e2e is the end-to-end test harness: whole agent runs against a
// scripted fake provider, driving the real service, sessions, tools and
// loop — the stack below the UI, exercised as one machine (ZCode's
// e2e/ suites, sized for Zgo).
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// Turn is one scripted assistant turn: tool calls then a final text.
type Turn struct {
	ToolCalls []ToolCall
	Text      string
}

// ToolCall is one scripted call in a turn.
type ToolCall struct {
	Name string
	Args string
}

// Script replies to every request with the next turn; requests are
// recorded for assertions. Beyond the scripted turns it replays the last
// one, so runaway loops surface as odd history, not a hang.
type Script struct {
	mu       sync.Mutex
	turns    []Turn
	next     int
	Requests []ai.Request

	// PreStream, when set, runs synchronously right after a request is
	// recorded — on the loop's goroutine, mid-run. n is the 1-based
	// request number. This is the deterministic in-flight moment steering
	// tests queue into.
	PreStream func(n int)
}

// Stream implements ai.Provider.
func (s *Script) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	s.mu.Lock()
	s.Requests = append(s.Requests, req)
	idx := s.next
	if idx >= len(s.turns) {
		idx = len(s.turns) - 1
	}
	s.next++
	n := s.next
	turn := s.turns[idx]
	pre := s.PreStream
	s.mu.Unlock()
	if pre != nil {
		pre(n)
	}

	out := make(chan ai.StreamEvent, 16)
	go func() {
		defer close(out)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		for _, call := range turn.ToolCalls {
			id := fmt.Sprintf("call_%s_%d", call.Name, n)
			out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: id, ToolCallName: call.Name}
			out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: id, ToolCallArgs: call.Args}
		}
		if turn.Text != "" {
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: turn.Text}
		}
		stop := ai.StopEndTurn
		if len(turn.ToolCalls) > 0 {
			stop = ai.StopToolUse
		}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: stop,
			Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}
	}()
	return out
}

// Harness is one e2e run: a service over a temp data dir, a session in a
// temp workspace, and the scripted provider wired in.
type Harness struct {
	t         *testing.T
	Svc       *service.AgentService
	Script    *Script
	SessionID string
	Workspace string
}

// New boots the harness. The configured provider is swapped for the
// script, and permission asks are auto-allowed — tests exercising the ask
// flow install their own asker through Svc.SetAsker.
func New(t *testing.T, turns ...Turn) *Harness {
	t.Helper()
	dataDir := t.TempDir()
	ws := t.TempDir()

	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	t.Cleanup(svc.Close)

	script := &Script{turns: turns}
	svc.UseProviderForTesting(func() ai.Provider { return script })
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })
	svc.SetAsker(func(agent.AskRequest) agent.AskResponse {
		return agent.AskResponse{} // allow this call, no session rule
	})

	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return &Harness{t: t, Svc: svc, Script: script, SessionID: info.ID, Workspace: ws}
}

// Ask runs one chat turn and collects the events.
func (h *Harness) Ask(text string) []agent.Event {
	h.t.Helper()
	var events []agent.Event
	err := h.Svc.Chat(h.SessionID, text, func(ev agent.Event) { events = append(events, ev) })
	if err != nil {
		h.t.Fatalf("chat: %v", err)
	}
	return events
}

// AskSteered asks and queues the steering text mid-run: at the second
// provider request — after the first tool batch, with the loop between
// drains — the script calls Steer from the loop's own goroutine. The
// queued message enters the transcript at the next turn boundary, so it
// is on the third request.
func (h *Harness) AskSteered(text, steering string) []agent.Event {
	h.t.Helper()
	steered := false
	h.Script.PreStream = func(n int) {
		if n == 2 && !steered {
			steered = true
			if err := h.Svc.Steer(h.SessionID, steering); err != nil {
				h.t.Errorf("steer: %v", err)
			}
		}
	}
	defer func() { h.Script.PreStream = nil }()
	return h.Ask(text)
}

// ReadWorkspace reads a file from the harness workspace.
func (h *Harness) ReadWorkspace(rel string) string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.Workspace, rel))
	if err != nil {
		h.t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

// WriteWorkspace plants a file in the harness workspace.
func (h *Harness) WriteWorkspace(rel, content string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.Workspace, rel), []byte(content), 0o644); err != nil {
		h.t.Fatalf("writing %s: %v", rel, err)
	}
}

// WorkspaceHas reports whether a file exists in the workspace.
func (h *Harness) WorkspaceHas(rel string) bool {
	_, err := os.Stat(filepath.Join(h.Workspace, rel))
	return err == nil
}

// HasEvents reports whether any collected event matches kind (and text,
// when non-empty, against Text or ResultContent).
func HasEvents(events []agent.Event, kind agent.EventKind, text string) bool {
	for _, ev := range events {
		if ev.Kind != kind {
			continue
		}
		if text == "" || strings.Contains(ev.Text, text) || strings.Contains(ev.ResultContent, text) {
			return true
		}
	}
	return false
}

// Marshal formats scripted call args (kept for readability at call sites).
func Marshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
