package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestE2EToolRoundTrip: ask → scripted write → file lands → the tool
// result returns to the model → final text, all through the real
// service/session/loop/permission/tool stack.
func TestE2EToolRoundTrip(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "notes.txt", "content": "第一行\n第二行"}),
		}}},
		Turn{Text: "已写入 notes.txt"},
	)

	events := h.Ask("把笔记写进 notes.txt")

	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if got := h.ReadWorkspace("notes.txt"); !strings.Contains(got, "第二行") {
		t.Fatalf("file = %q", got)
	}
	if len(h.Script.Requests) != 2 {
		t.Fatalf("provider called %d times, want 2", len(h.Script.Requests))
	}
	// 第二次请求把工具结果带回模型手里。
	found := false
	for _, m := range h.Script.Requests[1].Messages {
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockToolResult && strings.Contains(b.Text, "wrote notes.txt") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("tool result missing from the second request")
	}
}

// TestE2EPermissionAskDenies: a write the asker refuses never touches the
// workspace; the denial reaches the model as an error result and the run
// still ends normally.
func TestE2EPermissionAskDenies(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "secret.txt", "content": "x"}),
		}}},
		Turn{Text: "写入被拒绝，改为只读"},
	)
	h.Svc.SetAsker(func(agent.AskRequest) agent.AskResponse {
		return agent.AskResponse{Deny: true, Note: "测试拒绝"}
	})

	events := h.Ask("写 secret.txt")

	denied := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "write" && ev.IsError {
			denied = true
			if !strings.Contains(ev.ResultContent, "用户拒绝了这次调用") {
				t.Fatalf("deny result = %q", ev.ResultContent)
			}
		}
	}
	if !denied {
		t.Fatalf("no denied tool result: %+v", events)
	}
	if h.WorkspaceHas("secret.txt") {
		t.Fatal("denied write landed on disk")
	}
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end after a denial: %+v", events)
	}
}

// TestE2ESteeringMidRun: a message queued while the run is in flight
// enters the transcript at the next turn boundary and reaches the
// following provider request.
func TestE2ESteeringMidRun(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"a.txt"}`}}},
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"a.txt"}`}}},
		Turn{Text: "带着插话完成"},
	)
	h.WriteWorkspace("a.txt", "内容")

	events := h.AskSteered("看看 a.txt", "顺便看看字数")

	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if len(h.Script.Requests) != 3 {
		t.Fatalf("provider called %d times, want 3", len(h.Script.Requests))
	}
	steered := false
	for _, m := range h.Script.Requests[2].Messages {
		if m.Role == ai.RoleUser && strings.Contains(m.Text(), "顺便看看字数") {
			steered = true
		}
	}
	if !steered {
		t.Fatal("steering missing from the third request")
	}
}

// TestE2ECompactKeepsTail: manual compaction summarizes the old prefix
// through the provider and the next run continues from summary + tail.
// DefaultKeepTail is 6, so the session needs 8 messages (4 Q&A pairs)
// before there is anything to summarize.
func TestE2ECompactKeepsTail(t *testing.T) {
	h := New(t,
		Turn{Text: "回答一"},
		Turn{Text: "回答二"},
		Turn{Text: "回答三"},
		Turn{Text: "回答四"},
		Turn{Text: "这是摘要"},
		Turn{Text: "压缩后继续"},
	)
	for i := 1; i <= 4; i++ {
		h.Ask(fmt.Sprintf("第%d问", i))
	}
	if err := h.Svc.CompactSession(h.SessionID); err != nil {
		t.Fatalf("compact: %v", err)
	}
	events := h.Ask("继续")

	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("post-compact run failed: %+v", events)
	}
	summarized := false
	for _, m := range h.Script.Requests[len(h.Script.Requests)-1].Messages {
		if m.Role == ai.RoleUser && strings.Contains(m.Text(), "会话已压缩") &&
			strings.Contains(m.Text(), "这是摘要") {
			summarized = true
		}
	}
	if !summarized {
		t.Fatal("summary missing from the post-compact request")
	}
}
