package e2e

import (
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestE2EAttachmentReachesProvider: an image attached in the composer
// (ChatMedia) rides through service and session into the provider request
// — after the text, with media and data intact — and persists in the
// transcript.
func TestE2EAttachmentReachesProvider(t *testing.T) {
	h := New(t, Turn{Text: "图里是一个三角形"})
	img := ai.Block{Kind: ai.BlockImage, Media: "image/png", Data: "aGk="}

	var events []agent.Event
	err := h.Svc.ChatMedia(h.SessionID, "看看这张图", []ai.Block{img}, func(ev agent.Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if len(h.Script.Requests) != 1 {
		t.Fatalf("provider called %d times", len(h.Script.Requests))
	}
	blocks := h.Script.Requests[0].Messages[0].Blocks
	if len(blocks) != 2 || blocks[0].Kind != ai.BlockText || blocks[1].Kind != ai.BlockImage ||
		blocks[1].Media != "image/png" || blocks[1].Data != "aGk=" {
		t.Fatalf("request blocks = %+v", blocks)
	}

	msgs, err := h.Svc.Messages(h.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 1 || len(msgs[0].Blocks) != 2 || msgs[0].Blocks[1].Kind != ai.BlockImage {
		t.Fatalf("transcript blocks = %+v", msgs[0].Blocks)
	}
}
