package e2e

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestE2EAskCarriesPreviewDiff: a scripted write that hits the permission
// ask carries a preview diff; denying keeps the file off the disk (the
// preview never wrote).
func TestE2EAskCarriesPreviewDiff(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "asked.txt", "content": "将在批准后出现"}),
		}}},
		Turn{Text: "等待批准"},
	)

	var got agent.AskRequest
	h.Svc.SetAsker(func(req agent.AskRequest) agent.AskResponse {
		got = req
		return agent.AskResponse{Deny: true, Note: "测试拒绝"}
	})

	events := h.Ask("写 asked.txt")

	if got.Tool != "write" || !strings.Contains(got.Diff, "+将在批准后出现") {
		t.Fatalf("ask = %+v", got)
	}
	if h.WorkspaceHas("asked.txt") {
		t.Fatal("preview wrote the file")
	}
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
}
