// Package e2e is the end-to-end test harness: whole agent runs against a
// scripted fake provider, driving the real service, sessions, tools and
// loop — the stack below the UI, exercised as one machine (ZCode's
// e2e/ suites, sized for Zgo).
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/remote"
	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// Turn is one scripted assistant turn: tool calls then a final text.
type Turn struct {
	ToolCalls      []ToolCall
	Text           string
	ErrorOnce      string
	BlockAfterText bool
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
	errOnce  map[int]bool
	Requests []ai.Request

	// PreStream, when set, runs synchronously right after a request is
	// recorded — on the loop's goroutine, mid-run. n is the 1-based
	// request number. This is the deterministic in-flight moment steering
	// tests queue into.
	PreStream func(n int)
}

// failedOnce 注入并记录一次性错误；重试再次进入 Stream 时返回 true。
func (s *Script) failedOnce(idx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errOnce == nil {
		s.errOnce = map[int]bool{}
	}
	if s.errOnce[idx] {
		return true
	}
	s.errOnce[idx] = true
	return false
}

// Stream implements ai.Provider.
func (s *Script) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
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

	if turn.ErrorOnce != "" && !s.failedOnce(idx) {
		out := make(chan ai.StreamEvent, 3)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError,
			ErrorMessage: turn.ErrorOnce, Retryable: true, RetryAfterMS: 1}
		close(out)
		return out
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
		if turn.BlockAfterText {
			<-ctx.Done()
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopAborted}
			return
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
	Workspace string // 本地模式的工作区；远程模式即服务器侧磁盘根

	// Probe 是远程模式的断言通道（与服务同服务器的第二条连接）；
	// 本地模式为 nil。
	Probe *remote.Client
}

// wire swaps in the scripted provider and the auto-allow asker.
func wire(t *testing.T, svc *service.AgentService, turns []Turn) *Script {
	t.Helper()
	script := &Script{turns: turns}
	svc.UseProviderForTesting(func() ai.Provider { return script })
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })
	svc.SetAsker(func(agent.AskRequest) agent.AskResponse {
		return agent.AskResponse{} // allow this call, no session rule
	})
	return script
}

// New boots the harness. The configured provider is swapped for the
// script, and permission asks are auto-allowed — tests exercising the ask
// flow install their own asker through Svc.SetAsker.
func New(t *testing.T, turns ...Turn) *Harness {
	t.Helper()
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	t.Cleanup(svc.Close)
	script := wire(t, svc, turns)

	// 自动命名会向脚本 provider 多发一个小额补全请求——吃掉脚本回合、
	// 破坏计数断言（其行为由 service 专测覆盖）。e2e 关掉。
	svc.DisableAutoTitleForTesting()
	ws := t.TempDir()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return &Harness{t: t, Svc: svc, Script: script, SessionID: info.ID, Workspace: ws}
}

// NewRemote boots the harness against an in-process SSH workspace: the
// session target is ssh://（凭据经 ZGO_SSH_KEY，与服务真实拨号路径一
// 致），每个工具都走网线到 remotetest 服务器。Probe 提供根相对的
// 断言（ReadRemote/RemoteHas）。
func NewRemote(t *testing.T, turns ...Turn) *Harness {
	t.Helper()
	srv := remotetest.Start(t)
	host, port, err := net.SplitHostPort(srv.Address)
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	t.Setenv("ZGO_SSH_KEY", srv.KeyPEM) // openWorkspace 的凭据通道
	target := "ssh://tester@" + net.JoinHostPort(host, port) +
		"/" + strings.ReplaceAll(filepath.ToSlash(srv.Root), "\\", "/")

	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	t.Cleanup(svc.Close)
	script := wire(t, svc, turns)

	info, err := svc.NewSession(target)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	probe, err := remote.Dial(remote.Config{
		User: "tester", Host: host, Port: port,
		Key: srv.KeyPEM, Root: srv.Root,
	})
	if err != nil {
		t.Fatalf("probe dial: %v", err)
	}
	t.Cleanup(func() { probe.Close() })
	return &Harness{t: t, Svc: svc, Script: script, SessionID: info.ID,
		Workspace: srv.Root, Probe: probe}
}

// ReadRemote reads a workspace-root-relative file over the probe client.
func (h *Harness) ReadRemote(rel string) string {
	h.t.Helper()
	if h.Probe == nil {
		h.t.Fatal("ReadRemote on a local harness")
	}
	data, err := h.Probe.ReadFile(rel)
	if err != nil {
		h.t.Fatalf("probe read %s: %v", rel, err)
	}
	return string(data)
}

// RemoteHas reports whether a root-relative file exists remotely.
func (h *Harness) RemoteHas(rel string) bool {
	h.t.Helper()
	if h.Probe == nil {
		h.t.Fatal("RemoteHas on a local harness")
	}
	_, err := h.Probe.ReadFile(rel)
	return err == nil
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
