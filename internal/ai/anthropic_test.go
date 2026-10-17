package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer serves one scripted SSE body.
func sseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, events <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var got []StreamEvent
	for ev := range events {
		got = append(got, ev)
	}
	return got
}

// TestStreamTextAndUsage verifies the full happy path mapping.
func TestStreamTextAndUsage(t *testing.T) {
	srv := sseServer(t, 200, strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n"))

	p := NewAnthropic(srv.URL, "sk-test")
	events := collect(t, p.Stream(context.Background(), Request{Model: "m"}))

	last := events[len(events)-1]
	if last.Kind != EvMessageEnd {
		t.Fatalf("last event = %+v", last)
	}
	var text string
	for _, ev := range events {
		if ev.Kind == EvTextDelta {
			text += ev.TextDelta
		}
	}
	if text != "Hi there" {
		t.Fatalf("text = %q", text)
	}
	if last.StopReason != StopEndTurn || last.Usage.InputTokens != 25 || last.Usage.OutputTokens != 7 {
		t.Fatalf("end = %+v", last)
	}
	// message_delta 已宣告结束；message_stop 不得再产生第二个 end。
	ends := 0
	for _, ev := range events {
		if ev.Kind == EvMessageEnd {
			ends++
		}
	}
	if ends != 1 {
		t.Fatalf("message_end emitted %d times", ends)
	}
}

// TestStreamToolCall verifies tool_use blocks map to call events.
func TestStreamToolCall(t *testing.T) {
	srv := sseServer(t, 200, strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c1","name":"read"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pa"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"th\":\"a\"}"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		``,
	}, "\n"))

	p := NewAnthropic(srv.URL, "sk-test")
	events := collect(t, p.Stream(context.Background(), Request{Model: "m"}))

	var args string
	ended := StreamEvent{}
	for _, ev := range events {
		switch ev.Kind {
		case EvToolCallStart:
			if ev.ToolCallID != "c1" || ev.ToolCallName != "read" {
				t.Fatalf("call start = %+v", ev)
			}
		case EvToolCallDelta:
			args += ev.ToolCallArgs
		case EvMessageEnd:
			ended = ev
		}
	}
	if args != `{"path":"a"}` {
		t.Fatalf("assembled args = %q", args)
	}
	if ended.StopReason != StopToolUse {
		t.Fatalf("stop = %q", ended.StopReason)
	}
}

// TestStreamHTTPError verifies non-200 responses become error ends.
func TestStreamHTTPError(t *testing.T) {
	srv := sseServer(t, 401, `{"error":{"type":"authentication_error","message":"invalid key"}}`)
	p := NewAnthropic(srv.URL, "sk-bad")
	events := collect(t, p.Stream(context.Background(), Request{Model: "m"}))

	last := events[len(events)-1]
	if last.Kind != EvMessageEnd || last.StopReason != StopError {
		t.Fatalf("end = %+v", last)
	}
	if !strings.Contains(last.ErrorMessage, "invalid key") {
		t.Fatalf("error message = %q", last.ErrorMessage)
	}
	// 401 是永久性错误：不可重试。
	if last.Retryable {
		t.Fatalf("401 should not be retryable: %+v", last)
	}
}

// TestStreamHTTPRetryable verifies 429/5xx carry Retryable and the
// Retry-After header so the agent loop can back off.
func TestStreamHTTPRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After-Ms", "1200")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	t.Cleanup(srv.Close)
	p := NewAnthropic(srv.URL, "sk-test")
	events := collect(t, p.Stream(context.Background(), Request{Model: "m"}))

	last := events[len(events)-1]
	if last.StopReason != StopError {
		t.Fatalf("end = %+v", last)
	}
	if !last.Retryable {
		t.Fatalf("429 should be retryable: %+v", last)
	}
	if last.RetryAfterMS != 1200 {
		t.Fatalf("retryAfterMS = %d, want 1200", last.RetryAfterMS)
	}
}

// TestStreamSSEError verifies in-stream error events.
func TestStreamSSEError(t *testing.T) {
	srv := sseServer(t, 200, strings.Join([]string{
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
		``,
	}, "\n"))
	p := NewAnthropic(srv.URL, "sk-test")
	events := collect(t, p.Stream(context.Background(), Request{Model: "m"}))

	last := events[len(events)-1]
	if last.StopReason != StopError || !strings.Contains(last.ErrorMessage, "overloaded") {
		t.Fatalf("end = %+v", last)
	}
	// overloaded_error 是瞬态错误：必须标记可重试，否则 agent loop
	// 对 Anthropic 的重试链路整体断开。
	if !last.Retryable {
		t.Fatalf("overloaded_error should be retryable: %+v", last)
	}
}

// TestStreamAbort verifies cancellation ends the stream as aborted.
func TestStreamAbort(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		// Then stall until the test is done; the client must give up on
		// cancel without waiting for the server.
		<-release
	}))
	t.Cleanup(srv.Close)                 // runs second
	t.Cleanup(func() { close(release) }) // runs first: cleanups are LIFO

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	p := NewAnthropic(srv.URL, "sk-test")
	events := collect(t, p.Stream(ctx, Request{Model: "m"}))

	last := events[len(events)-1]
	if last.Kind != EvMessageEnd || last.StopReason != StopAborted {
		t.Fatalf("end = %+v", last)
	}
}

// TestMissingAPIKey verifies configuration failures surface as events.
func TestMissingAPIKey(t *testing.T) {
	p := NewAnthropic("http://unused", "")
	events := collect(t, p.Stream(context.Background(), Request{}))
	if len(events) != 1 || events[0].StopReason != StopError {
		t.Fatalf("events = %+v", events)
	}
}

// TestToWire verifies transcript translation, tool results in particular.
func TestToWire(t *testing.T) {
	wire := toWire([]Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "hi"}}},
		{Role: RoleAssistant, Blocks: []Block{
			{Kind: BlockText, Text: "looking"},
			{Kind: BlockToolCall, ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
		}},
		{Role: RoleToolResult, Blocks: []Block{
			{Kind: BlockToolResult, ToolCallID: "c1", Text: "contents", IsError: false},
		}},
	})
	if len(wire) != 3 {
		t.Fatalf("wire = %+v", wire)
	}
	if wire[0].Role != "user" || wire[0].Content[0].Text != "hi" {
		t.Fatalf("user = %+v", wire[0])
	}
	a := wire[1]
	if a.Role != "assistant" || len(a.Content) != 2 ||
		a.Content[1].ID != "c1" || string(a.Content[1].Input) != `{"path":"a"}` {
		t.Fatalf("assistant = %+v", a)
	}
	tr := wire[2]
	if tr.Role != "user" || tr.Content[0].Type != "tool_result" ||
		tr.Content[0].ToolUseID != "c1" ||
		len(tr.Content[0].ResultContent) != 1 ||
		tr.Content[0].ResultContent[0].Text != "contents" {
		t.Fatalf("tool result = %+v", tr)
	}
}
