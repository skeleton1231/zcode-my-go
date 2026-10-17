package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureServer records the request body and replies with fixed SSE.
func captureServer(t *testing.T, reply func(w http.ResponseWriter)) (*httptest.Server, *string) {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		reply(w)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

// TestAnthropicThinkingRequestAndStream verifies the budget parameter, the
// thinking/signature deltas, and the thinking block riding back in history.
func TestAnthropicThinkingRequestAndStream(t *testing.T) {
	srv, body := captureServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join([]string{
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先想想"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"答案"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
			``,
		}, "\n"))
	})
	p := NewAnthropic(srv.URL, "sk-test")

	var think, sig, text string
	for ev := range p.Stream(context.Background(), Request{
		Model: "m", MaxTokens: 8192, Thinking: ThinkingMedium,
		Messages: []Message{{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "hi"}}}},
	}) {
		switch ev.Kind {
		case EvThinkingDelta:
			think += ev.ThinkingText
			sig += ev.Signature
		case EvTextDelta:
			text += ev.TextDelta
		}
	}
	if think != "先想想" || sig != "sig-abc" || text != "答案" {
		t.Fatalf("think=%q sig=%q text=%q", think, sig, text)
	}

	// 请求体：预算与 max_tokens 抬升。
	var req struct {
		MaxTokens int `json:"max_tokens"`
		Thinking  *struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal([]byte(*body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Thinking == nil || req.Thinking.BudgetTokens != 12288 || req.Thinking.Type != "enabled" {
		t.Fatalf("thinking param = %+v", req.Thinking)
	}
	if req.MaxTokens <= 12288 {
		t.Fatalf("max_tokens %d must exceed the budget", req.MaxTokens)
	}
}

// TestAnthropicThinkingOffOmitsParam verifies Off sends no thinking field.
func TestAnthropicThinkingOffOmitsParam(t *testing.T) {
	srv, body := captureServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
	})
	p := NewAnthropic(srv.URL, "sk-test")
	for range p.Stream(context.Background(), Request{Model: "m", Messages: []Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "hi"}}}}}) {
	}
	if strings.Contains(*body, "\"thinking\"") {
		t.Fatalf("off level still sent thinking: %s", *body)
	}
}

// TestAnthropicThinkingBlockRidesBack verifies toWire keeps thinking with
// its signature (required for thinking + tool use continuity).
func TestAnthropicThinkingBlockRidesBack(t *testing.T) {
	wire := toWire([]Message{
		{Role: RoleAssistant, Blocks: []Block{
			{Kind: BlockThinking, Text: "推理", Signature: "sig-1"},
			{Kind: BlockText, Text: "回答"},
		}},
	})
	b := wire[0].Content[0]
	if b.Type != "thinking" || b.Thinking != "推理" || b.Signature != "sig-1" {
		t.Fatalf("thinking block = %+v", b)
	}
}

// TestOpenAIReasoningEffort verifies the o-series effort parameter.
func TestOpenAIReasoningEffort(t *testing.T) {
	srv, body := captureServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	})
	p := NewOpenAI(srv.URL, "sk-test")
	for range p.Stream(context.Background(), Request{Model: "o", Thinking: ThinkingHigh}) {
	}
	if !strings.Contains(*body, `"reasoning_effort":"high"`) {
		t.Fatalf("effort missing: %s", *body)
	}
}

// TestGeminiThinkingBudget verifies thinkingConfig is sent per level.
func TestGeminiThinkingBudget(t *testing.T) {
	srv, body := captureServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{}]},\"finishReason\":\"STOP\"}]}\n\n")
	})
	p := NewGemini(srv.URL, "k")
	for range p.Stream(context.Background(), Request{Model: "gemini-3-flash", Thinking: ThinkingLow}) {
	}
	if !strings.Contains(*body, `"thinkingConfig":{"thinkingBudget":2048}`) {
		t.Fatalf("budget missing: %s", *body)
	}
}
