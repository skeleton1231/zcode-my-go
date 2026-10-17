package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	// OAuthToken, when set and APIKey is empty, authenticates as a
	// subscription (Authorization: Bearer) instead of an API key.
	OAuthToken string
	HTTP       *http.Client
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
	endRetryable := func(reason StopReason, message string, retryable bool, afterMS int) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason, ErrorMessage: message,
			Retryable: retryable, RetryAfterMS: afterMS}
	}
	if a.APIKey == "" && a.OAuthToken == "" {
		end(StopError, "missing API key: set it in Settings or ANTHROPIC_API_KEY, or /login")
		return
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	budget := anthropicThinkingBudget(req.Thinking)
	if budget > 0 && maxTokens <= budget+1024 {
		maxTokens = budget + 4096 // API 要求 max_tokens 大于思考预算
	}
	body, err := json.Marshal(wireRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		System:    req.System,
		Messages:  toWire(req.Messages),
		Tools:     req.Tools,
		Stream:    true,
		Thinking:  newWireThinking(budget),
	})
	if err != nil {
		end(StopError, fmt.Sprintf("encoding request: %v", err))
		return
	}

	if os.Getenv("ZGO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[zgo-debug] request: %s\n", body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		end(StopError, fmt.Sprintf("building request: %v", err))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	if a.OAuthToken != "" && a.APIKey == "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.OAuthToken)
	} else {
		httpReq.Header.Set("x-api-key", a.APIKey)
		httpReq.Header.Set("Authorization", "Bearer "+a.APIKey)
	}

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
		// 429/5xx 属瞬态故障：与 Gemini/OpenAI 一致地携带 Retryable 与
		// Retry-After；否则 agent loop 对主 provider（Anthropic）永不重试。
		msg, status := httpErrorStatus(resp)
		endRetryable(StopError, msg, retryableStatus(status), retryAfterMS(resp.Header))
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
			out <- StreamEvent{Kind: EvMessageEnd, StopReason: StopError, ErrorMessage: err.Error(),
				Retryable: errRetryable(err)}
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
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
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
		case "thinking_delta":
			if ev.Delta.Thinking != "" {
				out <- StreamEvent{Kind: EvThinkingDelta, ThinkingText: ev.Delta.Thinking}
			}
		case "signature_delta":
			if ev.Delta.Signature != "" {
				out <- StreamEvent{Kind: EvThinkingDelta, Signature: ev.Delta.Signature}
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
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("parsing message_delta: %w", err)
		}
		usage.OutputTokens = ev.Usage.OutputTokens
		// 兼容端点（如 GLM）不在 message_start 报 input_tokens，只在
		// message_delta 补报；不覆盖已取得的非零值。
		if usage.InputTokens == 0 && ev.Usage.InputTokens > 0 {
			usage.InputTokens = ev.Usage.InputTokens
		}
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
			wrapped := fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
			// overloaded_error 等瞬态错误必须包成 retryableError，
			// 否则 parseSSE 的 errRetryable 恒为 false，重试链路断开。
			if retryableKind(ev.Error.Type) {
				return false, &retryableError{err: wrapped}
			}
			return false, wrapped
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
	Thinking  *wireThinking     `json:"thinking,omitempty"`
}

// wireThinking enables extended thinking with a token budget.
type wireThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// anthropicThinkingBudget maps levels to budgets; Off disables (nil).
func anthropicThinkingBudget(level ThinkingLevel) int {
	switch level {
	case ThinkingLow:
		return 4096
	case ThinkingMedium:
		return 12288
	case ThinkingHigh:
		return 24576
	default:
		return 0
	}
}

func newWireThinking(budget int) *wireThinking {
	if budget <= 0 {
		return nil
	}
	return &wireThinking{Type: "enabled", BudgetTokens: budget}
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
	// thinking: reasoning text plus the provider signature passed back
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result: its payload is the standard content array, never a
	// private field — compatible endpoints drop anything else.
	ToolUseID     string      `json:"tool_use_id,omitempty"`
	ResultContent []wireBlock `json:"content,omitempty"`
	IsError       bool        `json:"is_error,omitempty"`
	Source        *wireSource `json:"source,omitempty"` // image
}

// wireSource is the base64 image source of an image block.
type wireSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// toWire translates the transcript to the wire format.
func toWire(messages []Message) []wireMessage {
	wire := make([]wireMessage, 0, len(messages))
	for _, m := range messages {
		wm := wireMessage{Role: string(m.Role)}
		switch m.Role {
		case RoleUser:
			for _, b := range m.Blocks {
				switch b.Kind {
				case BlockImage:
					wm.Content = append(wm.Content, wireBlock{Type: "image",
						Source: &wireSource{Type: "base64",
							MediaType: b.Media, Data: b.Data}})
				default:
					wm.Content = append(wm.Content, wireBlock{Type: "text", Text: b.Text})
				}
			}
		case RoleAssistant:
			for _, b := range m.Blocks {
				switch b.Kind {
				case BlockThinking:
					wm.Content = append(wm.Content, wireBlock{
						Type: "thinking", Thinking: b.Text, Signature: b.Signature})
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
					ResultContent: []wireBlock{{Type: "text", Text: b.Text}},
				})
			}
		}
		wire = append(wire, wm)
	}
	return wire
}
