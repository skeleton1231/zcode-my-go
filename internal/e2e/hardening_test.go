package e2e

import (
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestE2EParallelBatchOverlaps: a write blocked in the permission ask
// must not stop the read (auto-allowed) from completing in the same
// batch — the parallel executor's contract, proven without wall-clock
// racing (ask is SerialOnly by design, so the blocked tool is a gated
// write; the ungated read proves overlap).
func TestE2EParallelBatchOverlaps(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{
			{Name: "write", Args: Marshal(map[string]string{"path": "gated.txt", "content": "x"})},
			{Name: "read", Args: Marshal(map[string]string{"path": "notes.txt"})},
		}},
		Turn{Text: "两件都完成"},
	)
	h.WriteWorkspace("notes.txt", "可读内容")

	answers := make(chan agent.AskResponse, 1)
	h.Svc.SetAsker(func(req agent.AskRequest) agent.AskResponse {
		return <-answers
	})

	var (
		mu      sync.Mutex
		readEnd bool
	)
	go func() {
		_ = h.Svc.Chat(h.SessionID, "写一个并读一个", func(ev agent.Event) {
			if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "read" {
				mu.Lock()
				readEnd = true
				mu.Unlock()
			}
		})
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := readEnd
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	done := readEnd
	mu.Unlock()
	if !done {
		t.Fatal("read did not complete while write's ask blocked — batch ran serially")
	}
	if h.WorkspaceHas("gated.txt") {
		t.Fatal("gated write landed before approval")
	}
	answers <- agent.AskResponse{Deny: true, Note: "测试拒绝"}
}

// TestE2EAbortMidStream: a provider that streams text then hangs ends
// aborted when the session is aborted, and the session chats again.
func TestE2EAbortMidStream(t *testing.T) {
	h := New(t,
		Turn{Text: "说到一半", BlockAfterText: true},
		Turn{Text: "恢复后的回答"},
	)

	var events []agent.Event
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.Svc.Chat(h.SessionID, "讲点什么", func(ev agent.Event) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		})
	}()

	// 等到文本到达再中止。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		saw := HasEvents(events, agent.EvTextDelta, "说到一半")
		mu.Unlock()
		if saw {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.Svc.Abort(h.SessionID)
	<-done

	mu.Lock()
	defer mu.Unlock()
	aborted := false
	for _, ev := range events {
		if ev.Kind == agent.EvAgentEnd && ev.StopReason == ai.StopAborted {
			aborted = true
		}
	}
	if !aborted {
		t.Fatalf("run did not end aborted: %+v", events)
	}

	// 会话可继续：下一轮正常收尾。
	follow := h.Ask("继续")
	if !HasEvents(follow, agent.EvAgentEnd, "") {
		t.Fatalf("post-abort chat failed: %+v", follow)
	}
	if !HasEvents(follow, agent.EvTextDelta, "恢复后的回答") {
		t.Fatalf("post-abort answer missing: %+v", follow)
	}
}

// TestE2ETransientRetrySucceeds: a retryable provider error on the first
// attempt surfaces as a turn-retry event, then the same turn answers.
func TestE2ETransientRetrySucceeds(t *testing.T) {
	h := New(t,
		Turn{Text: "最终回答", ErrorOnce: "429 too many requests"},
	)

	events := h.Ask("问一次")

	if !HasEvents(events, agent.EvTurnRetry, "第 1 次重试") {
		t.Fatalf("no retry event: %+v", events)
	}
	if !HasEvents(events, agent.EvTextDelta, "最终回答") {
		t.Fatalf("final answer missing: %+v", events)
	}
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatal("run did not end")
	}
	// 重试再次命中 provider：两次请求。
	if len(h.Script.Requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(h.Script.Requests))
	}
	if got := h.Script.Requests[1].Messages[0].Text(); got != "问一次" {
		t.Fatalf("retry replayed wrong prompt: %q", got)
	}
}
