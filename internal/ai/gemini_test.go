package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func geminiServer(t *testing.T, status int, chunks []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "" {
			w.WriteHeader(401)
			return
		}
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGeminiStreamText(t *testing.T) {
	srv := geminiServer(t, 200, []string{
		`{"candidates":[{"content":{"parts":[{"text":"你好"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"世界"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":2}}`,
	})
	p := NewGemini(srv.URL, "k")
	var events []StreamEvent
	for ev := range p.Stream(context.Background(), Request{Model: "gemini-x"}) {
		events = append(events, ev)
	}
	var text string
	for _, ev := range events {
		if ev.Kind == EvTextDelta {
			text += ev.TextDelta
		}
	}
	if text != "你好世界" {
		t.Fatalf("text = %q", text)
	}
	last := events[len(events)-1]
	if last.StopReason != StopEndTurn || last.Usage.InputTokens != 9 || last.Usage.OutputTokens != 2 {
		t.Fatalf("end = %+v", last)
	}
}

func TestGeminiStreamFunctionCall(t *testing.T) {
	srv := geminiServer(t, 200, []string{
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"a.go"}}}]},"finishReason":"STOP"}]}`,
	})
	p := NewGemini(srv.URL, "k")
	var events []StreamEvent
	for ev := range p.Stream(context.Background(), Request{Model: "m"}) {
		events = append(events, ev)
	}
	var start, delta StreamEvent
	for _, ev := range events {
		if ev.Kind == EvToolCallStart {
			start = ev
		}
		if ev.Kind == EvToolCallDelta {
			delta = ev
		}
	}
	if start.ToolCallName != "read" || start.ToolCallID == "" {
		t.Fatalf("start = %+v", start)
	}
	if delta.ToolCallArgs != `{"path":"a.go"}` {
		t.Fatalf("args = %q", delta.ToolCallArgs)
	}
}

func TestToGeminiPairsResponsesWithNames(t *testing.T) {
	contents := toGemini([]Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "hi"}}},
		{Role: RoleAssistant, Blocks: []Block{
			{Kind: BlockToolCall, ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
		}},
		{Role: RoleToolResult, Blocks: []Block{
			{Kind: BlockToolResult, ToolCallID: "c1", Text: "body"},
		}},
	})
	if len(contents) != 3 {
		t.Fatalf("contents = %d", len(contents))
	}
	call := contents[1].Parts[0].FunctionCall
	if call.Name != "read" || call.Args["path"] != "a" {
		t.Fatalf("call = %+v", call)
	}
	resp := contents[2].Parts[0].FunctionResponse
	if resp.Name != "read" || resp.Response["output"] != "body" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestGeminiHTTPError(t *testing.T) {
	srv := geminiServer(t, 400, []string{`{"error":{"message":"bad key"}}`})
	p := NewGemini(srv.URL, "bad")
	var events []StreamEvent
	for ev := range p.Stream(context.Background(), Request{Model: "m"}) {
		events = append(events, ev)
	}
	last := events[len(events)-1]
	if last.StopReason != StopError || !strings.Contains(last.ErrorMessage, "bad key") {
		t.Fatalf("end = %+v", last)
	}
}
