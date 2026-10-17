package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func openAIServer(t *testing.T, status int, chunks []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
		if status == 200 {
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAIStreamText(t *testing.T) {
	srv := openAIServer(t, 200, []string{
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"delta":{"content":"！"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3}}`,
	})
	p := NewOpenAI(srv.URL, "sk-test")
	var events []StreamEvent
	for ev := range p.Stream(context.Background(), Request{Model: "gpt"}) {
		events = append(events, ev)
	}
	var text string
	for _, ev := range events {
		if ev.Kind == EvTextDelta {
			text += ev.TextDelta
		}
	}
	if text != "你好！" {
		t.Fatalf("text = %q", text)
	}
	last := events[len(events)-1]
	if last.Kind != EvMessageEnd || last.StopReason != StopEndTurn ||
		last.Usage.InputTokens != 11 || last.Usage.OutputTokens != 3 {
		t.Fatalf("end = %+v", last)
	}
}

func TestOpenAIStreamToolCalls(t *testing.T) {
	srv := openAIServer(t, 200, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	p := NewOpenAI(srv.URL, "sk-test")
	var events []StreamEvent
	for ev := range p.Stream(context.Background(), Request{Model: "gpt"}) {
		events = append(events, ev)
	}
	var args string
	started, ended := false, StreamEvent{}
	for _, ev := range events {
		switch ev.Kind {
		case EvToolCallStart:
			started = ev.ToolCallID == "c1" && ev.ToolCallName == "read"
		case EvToolCallDelta:
			args += ev.ToolCallArgs
		case EvMessageEnd:
			ended = ev
		}
	}
	if !started || args != `{"path":"a"}` {
		t.Fatalf("tool call events wrong: started=%v args=%q", started, args)
	}
	if ended.StopReason != StopToolUse {
		t.Fatalf("finish = %q", ended.StopReason)
	}
}

func TestOpenAIHTTPError(t *testing.T) {
	srv := openAIServer(t, 500, []string{`{"error":{"message":"overloaded"}}`})
	p := NewOpenAI(srv.URL, "sk-test")
	var events []StreamEvent
	for ev := range p.Stream(context.Background(), Request{Model: "gpt"}) {
		events = append(events, ev)
	}
	last := events[len(events)-1]
	if last.StopReason != StopError || !strings.Contains(last.ErrorMessage, "overloaded") {
		t.Fatalf("end = %+v", last)
	}
}

func TestToOpenAI(t *testing.T) {
	msgs := toOpenAI("sys", []Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "hi"}}},
		{Role: RoleAssistant, Blocks: []Block{
			{Kind: BlockToolCall, ID: "c1", Name: "read", Arguments: `{}`},
		}},
		{Role: RoleToolResult, Blocks: []Block{
			{Kind: BlockToolResult, ToolCallID: "c1", Text: "body"},
		}},
	})
	if len(msgs) != 4 || msgs[0].Role != "system" || msgs[0].Content != "sys" {
		t.Fatalf("system = %+v", msgs[0])
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 ||
		msgs[2].ToolCalls[0].ID != "c1" {
		t.Fatalf("assistant = %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" || msgs[3].Content != "body" {
		t.Fatalf("tool = %+v", msgs[3])
	}
}
