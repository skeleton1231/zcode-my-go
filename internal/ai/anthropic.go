package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// anthropicVersion is the API version every request declares. Compatible
// endpoints (GLM, Kimi) accept it unchanged.
const anthropicVersion = "2023-06-01"

// Anthropic speaks the Anthropic Messages API over SSE. It works with any
// compatible endpoint: point BaseURL at the provider's API root.
type Anthropic struct {
	BaseURL string // e.g. https://api.anthropic.com
	APIKey  string
	HTTP    *http.Client
}

// NewAnthropic returns a client with sane timeouts. Streaming requests are
// bounded by their context, not by the client timeout.
func NewAnthropic(baseURL, apiKey string) *Anthropic {
	return &Anthropic{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

// Stream implements Provider. Failures surface as a final EvMessageEnd with
// StopError; the channel is always closed exactly once.
func (a *Anthropic) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		a.stream(ctx, req, out)
	}()
	return out
}

func (a *Anthropic) stream(ctx context.Context, req Request, out chan<- StreamEvent) {
	end := func(reason StopReason, message string) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason, ErrorMessage: message}
	}
	if a.APIKey == "" {
		end(StopError, "missing API key: set it in Settings or ANTHROPIC_API_KEY")
		return
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body, err := json.Marshal(wireRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		System:    req.System,
		Messages:  toWire(req.Messages),
		Tools:     req.Tools,
		Stream:    true,
	})
	if err != nil {
		end(StopError, fmt.Sprintf("encoding request: %v", err))
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		end(StopError, fmt.Sprintf("building request: %v", err))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("Authorization", "Bearer "+a.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.HTTP.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			end(StopAborted, "")
			return
		}
		end(StopError, fmt.Sprintf("request failed: %v", err))
		return
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10)) // let the connection go
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		end(StopError, httpError(resp))
		return
	}

	// Cancellation must unblock a stalled stream: closing the body makes the
	// scanner fail, which parseSSE maps to an aborted end.
	watching := make(chan struct{})
	defer close(watching)
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-watching:
		}
	}()

	a.parseSSE(ctx, resp.Body, out)
}

// parseSSE reads the SSE body and forwards mapped events until the turn
// ends, the stream breaks, or the context is canceled. Exactly one
// EvMessageEnd leaves this function: message_delta and message_stop both
// announce the end, so the first one wins and parsing stops there.
func (a *Anthropic) parseSSE(ctx context.Context, body io.Reader, out chan<- StreamEvent) {
	usage := Usage{}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var event string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		ended, err := handleSSEData(strings.TrimPrefix(line, "data: "), event, &usage, out)
		switch {
		case err != nil:
			out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopError, ErrorMessage: err.Error()}
			return
		case ended:
			return
		case ctx.Err() != nil:
			out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopAborted, Usage: usage}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopAborted, Usage: usage}
			return
		}
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopError,
			ErrorMessage: fmt.Sprintf("stream broke: %v", err), Usage: usage}
		return
	}
	// A stream that ends without message_stop: treat it as a clean end turn,
	// since usage and stop reason were carried by earlier events.
	out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopEndTurn, Usage: usage}
}

// handleSSEData maps one SSE data payload to provider events. It reports
// whether the turn ended with it.
func handleSSEData(data, event string, usage *Usage, out chan<- StreamEvent) (ended bool, err error) {
	switch event {
	case "message_start":
		var ev struct {
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("parsing message_start: %w", err)
		}
		usage.InputTokens = ev.Message.Usage.InputTokens
		usage.CacheReadTokens = ev.Message.Usage.CacheReadInputTokens
		usage.CacheWriteTokens = ev.Message.Usage.CacheCreationInputTokens
		out <- StreamEvent{Kind: EvMessageStart, Usage: *usage}

	case "content_block_start":
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("parsing content_block_start: %w", err)
		}
		if ev.ContentBlock.Type == "tool_use" {
			out <- StreamEvent{Kind: EvToolCallStart,
				ToolCallID: ev.ContentBlock.ID, ToolCallName: ev.ContentBlock.Name}
		}

	case "content_block_delta":
		var ev struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("parsing content_block_delta: %w", err)
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				out <- StreamEvent{Kind: EvTextDelta, TextDelta: ev.Delta.Text}
			}
		case "input_json_delta":
			if ev.Delta.PartialJSON != "" {
				out <- StreamEvent{Kind: EvToolCallDelta, ToolCallArgs: ev.Delta.PartialJSON}
			}
		}

	case "message_delta":
		var ev struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("parsing message_delta: %w", err)
		}
		usage.OutputTokens = ev.Usage.OutputTokens
		if ev.Delta.StopReason != "" {
			out <- StreamEvent{Kind: EvMessageEnd,
				StopReason: StopReason(ev.Delta.StopReason), Usage: *usage}
			return true, nil
		}

	case "message_stop":
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopEndTurn, Usage: *usage}
		return true, nil

	case "error":
		var ev struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err == nil && ev.Error.Message != "" {
			return false, fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
		}
		return false, fmt.Errorf("provider error: %s", data)
	}
	return false, nil
}

// httpError reads a bounded error body into a readable message.
func httpError(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Sprintf("%d %s", resp.StatusCode, resp.Status)
	}
	// Endpoints return JSON errors; surface the message field when present.
	var structured struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &structured) == nil && structured.Error.Message != "" {
		msg = structured.Error.Message
	}
	return fmt.Sprintf("%d %s: %s", resp.StatusCode, resp.Status, msg)
}

// wireRequest is the Messages API request body.
type wireRequest struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	System    string            `json:"system,omitempty"`
	Messages  []wireMessage     `json:"messages"`
	Tools     []ToolDeclaration `json:"tools,omitempty"`
	Stream    bool              `json:"stream"`
}

// wireMessage is one message on the wire. Tool results travel as user
// messages holding tool_result blocks, which keeps the alternation the API
// requires.
type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

// wireBlock is one content block on the wire.
type wireBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// toWire translates the transcript to the wire format.
func toWire(messages []Message) []wireMessage {
	wire := make([]wireMessage, 0, len(messages))
	for _, m := range messages {
		wm := wireMessage{Role: string(m.Role)}
		switch m.Role {
		case RoleUser:
			for _, b := range m.Blocks {
				wm.Content = append(wm.Content, wireBlock{Type: "text", Text: b.Text})
			}
		case RoleAssistant:
			for _, b := range m.Blocks {
				switch b.Kind {
				case BlockText:
					wm.Content = append(wm.Content, wireBlock{Type: "text", Text: b.Text})
				case BlockToolCall:
					input := json.RawMessage(b.Arguments)
					if len(input) == 0 {
						input = json.RawMessage("{}")
					}
					wm.Content = append(wm.Content, wireBlock{
						Type: "tool_use", ID: b.ID, Name: b.Name, Input: input})
				}
			}
		case RoleToolResult:
			wm.Role = "user"
			for _, b := range m.Blocks {
				wm.Content = append(wm.Content, wireBlock{
					Type: "tool_result", ToolUseID: b.ToolCallID, IsError: b.IsError,
					Text: b.Text})
			}
		}
		wire = append(wire, wm)
	}
	return wire
}
