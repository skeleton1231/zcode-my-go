package e2e

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestE2EEditCarriesDiff: a scripted write followed by an edit produces
// tool events whose Diff fields hold a unified diff of the change —
// the write's diff is all additions (new file), the edit's shows the
// replaced line as -old +new.
func TestE2EEditCarriesDiff(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{
			{Name: "write", Args: Marshal(map[string]string{
				"path": "a.txt", "content": "alpha\nbeta\ngamma\n"})},
			{Name: "edit", Args: Marshal(map[string]string{
				"path": "a.txt", "old_string": "beta", "new_string": "BETA"})},
		}},
		Turn{Text: "改完了"},
	)

	events := h.Ask("写并改 a.txt")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	var writeDiff, editDiff string
	for _, ev := range events {
		if ev.Kind != agent.EvToolExecutionEnd || ev.Diff == "" {
			continue
		}
		switch ev.ToolName {
		case "write":
			writeDiff = ev.Diff
		case "edit":
			editDiff = ev.Diff
		}
	}
	if !strings.Contains(writeDiff, "+alpha") || strings.Contains(writeDiff, "-alpha") {
		t.Fatalf("write diff = %q", writeDiff)
	}
	if !strings.Contains(editDiff, "-beta") || !strings.Contains(editDiff, "+BETA") {
		t.Fatalf("edit diff = %q", editDiff)
	}
	if !strings.Contains(editDiff, " alpha") || !strings.Contains(editDiff, " gamma") {
		t.Fatalf("edit diff lacks context: %q", editDiff)
	}
}
