package ai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSmokeRealProvider runs one real streaming turn against a live
// Anthropic-compatible endpoint. Skipped unless ZGO_SMOKE_BASE_URL,
// ZGO_SMOKE_API_KEY and ZGO_SMOKE_MODEL are set — CI never configures them.
func TestSmokeRealProvider(t *testing.T) {
	base := os.Getenv("ZGO_SMOKE_BASE_URL")
	key := os.Getenv("ZGO_SMOKE_API_KEY")
	model := os.Getenv("ZGO_SMOKE_MODEL")
	if base == "" || key == "" || model == "" {
		t.Skip("smoke requires ZGO_SMOKE_BASE_URL, ZGO_SMOKE_API_KEY and ZGO_SMOKE_MODEL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewAnthropic(base, key)
	var text strings.Builder
	var end StreamEvent
	for ev := range p.Stream(ctx, Request{
		Model:     model,
		System:    "Answer in one short sentence.",
		Messages:  []Message{{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "用一句话介绍 Go 语言"}}}},
		MaxTokens: 512,
	}) {
		if ev.Kind == EvTextDelta {
			text.WriteString(ev.TextDelta)
		}
		if ev.Kind == EvMessageEnd {
			end = ev
		}
	}

	if end.Kind != EvMessageEnd {
		t.Fatalf("no message_end event")
	}
	if end.StopReason != StopEndTurn {
		t.Fatalf("stop = %q, err = %q", end.StopReason, end.ErrorMessage)
	}
	if text.Len() == 0 {
		t.Fatalf("empty response text")
	}
	if end.Usage.InputTokens == 0 || end.Usage.OutputTokens == 0 {
		t.Logf("usage not reported by endpoint: %+v", end.Usage)
	}
	t.Logf("reply: %s", text.String())
	t.Logf("usage: %+v", end.Usage)
}
