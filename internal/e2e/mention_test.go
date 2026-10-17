package e2e

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestE2EMentionExpandsIntoRequest: an @file mention in the message rides
// through the service into the provider request — the token stays, the
// file's content is fenced at the end.
func TestE2EMentionExpandsIntoRequest(t *testing.T) {
	h := New(t, Turn{Text: "收到"})
	h.WriteWorkspace("todo.txt", "买牛奶")

	events := h.Ask("参考 @todo.txt")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if len(h.Script.Requests) != 1 {
		t.Fatalf("provider called %d times", len(h.Script.Requests))
	}
	text := h.Script.Requests[0].Messages[0].Text()
	if !strings.Contains(text, "参考 @todo.txt") {
		t.Fatalf("token dropped: %q", text)
	}
	if !strings.Contains(text, "买牛奶") || !strings.Contains(text, "```") {
		t.Fatalf("content not attached: %q", text)
	}
}
