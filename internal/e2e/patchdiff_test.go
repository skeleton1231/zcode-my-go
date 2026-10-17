package e2e

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestE2EPatchAskCarriesMultiFileDiff: a multi-section patch hitting the
// permission ask previews every file's change; denial writes nothing.
func TestE2EPatchAskCarriesMultiFileDiff(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "apply_patch",
			Args: Marshal(map[string]string{"patch": "*** Begin Patch\n" +
				"*** Update File: base.txt\n@@\n-old line\n+new line\n" +
				"*** Add File: extra.txt\n+added\n" +
				"*** End Patch\n"}),
		}}},
		Turn{Text: "等待批准"},
	)
	h.WriteWorkspace("base.txt", "old line\n")

	var got agent.AskRequest
	h.Svc.SetAsker(func(req agent.AskRequest) agent.AskResponse {
		got = req
		return agent.AskResponse{Deny: true, Note: "测试拒绝"}
	})

	events := h.Ask("打个补丁")

	if got.Tool != "apply_patch" {
		t.Fatalf("ask tool = %s", got.Tool)
	}
	for _, want := range []string{"--- base.txt", "-old line", "+new line", "--- extra.txt", "+added"} {
		if !strings.Contains(got.Diff, want) {
			t.Fatalf("ask diff misses %q:\n%s", want, got.Diff)
		}
	}
	if got, _ := h.ReadWorkspace("base.txt"), 0; strings.Contains(got, "new line") {
		t.Fatal("preview wrote base.txt")
	}
	if h.WorkspaceHas("extra.txt") {
		t.Fatal("preview created extra.txt")
	}
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
}
