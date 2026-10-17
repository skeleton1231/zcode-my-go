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

// OpenAI speaks the Chat Completions API over SSE, translated from ZCode's
// provider adapters. It works with any OpenAI-compatible endpoint.
type OpenAI struct {
	BaseURL string // e.g. https://api.openai.com/v1
	APIKey  string
	HTTP    *http.Client
}

// NewOpenAI returns a client with sane timeouts.
func NewOpenAI(baseURL, apiKey string) *OpenAI {
	return &OpenAI{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

// Stream implements Provider with the same failure contract as Anthropic.
func (a *OpenAI) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		a.stream(ctx, req, out)
	}()
	return out
}

func (a *OpenAI) stream(ctx context.Context, req Request, out chan<- StreamEvent) {
	end := func(reason StopReason, message string) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason, ErrorMessage: message}
	}
	endRetryable := func(reason StopReason, message string, retryable bool, afterMS int) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason, ErrorMessage: message,
			Retryable: retryable, RetryAfterMS: afterMS}
	}
	if a.APIKey == "" {
		end(StopError, "missing API key: set it in Settings or OPENAI_API_KEY")
		return
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body, err := json.Marshal(openaiRequest{
		Model:    req.Model,
		Messages: toOpenAI(req.System, req.Messages),
		Tools:    toOpenAITools(req.Tools),
		Stream:   true,
		StreamOptions: &openaiStreamOptions{
			IncludeUsage: true,
		},
		MaxTokens: maxTokens,
	})
	if err != nil {
		end(StopError, fmt.Sprintf("encoding request: %v", err))
		return
	}
	if os.Getenv("ZGO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[zgo-debug] openai request: %s\n", body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		end(StopError, fmt.Sprintf("building request: %v", err))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.APIKey)

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
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		msg, status := httpErrorStatus(resp)
		endRetryable(StopError, msg, retryableStatus(status), retryAfterMS(resp.Header))
		return
	}

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

// parseSSE reads Chat Completions SSE chunks until [DONE], mapping deltas
// to provider events with exactly one message_end.
func (a *OpenAI) parseSSE(ctx context.Context, body io.Reader, out chan<- StreamEvent) {
	usage := Usage{}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	ended := false
	emitEnd := func(reason StopReason, message string) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason,
			ErrorMessage: message, Usage: usage}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			emitEnd(StopEndTurn, "")
			ended = true
			return
		}
		var chunk openaiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 跳过无法解析的块，流不中断
		}
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			out <- StreamEvent{Kind: EvTextDelta, TextDelta: choice.Delta.Content}
		}
		for _, call := range choice.Delta.ToolCalls {
			if call.ID != "" {
				out <- StreamEvent{Kind: EvToolCallStart,
					ToolCallID: call.ID, ToolCallName: call.Function.Name}
			}
			if call.Function.Arguments != "" {
				out <- StreamEvent{Kind: EvToolCallDelta,
					ToolCallArgs: call.Function.Arguments}
			}
		}
		if choice.FinishReason != "" {
			emitEnd(mapFinishReason(choice.FinishReason), "")
			ended = true
			return
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			emitEnd(StopAborted, "")
			return
		}
		emitEnd(StopError, fmt.Sprintf("stream broke: %v", err))
		return
	}
	if !ended {
		emitEnd(StopEndTurn, "")
	}
}

func mapFinishReason(reason string) StopReason {
	switch reason {
	case "tool_calls":
		return StopToolUse
	case "length":
		return StopLength
	default:
		return StopEndTurn
	}
}

// --- wire types ----------------------------------------------------------

type openaiRequest struct {
	Model         string               `json:"model"`
	Messages      []openaiMessage      `json:"messages"`
	Tools         []openaiTool         `json:"tools,omitempty"`
	Stream        bool                 `json:"stream"`
	StreamOptions *openaiStreamOptions `json:"stream_options,omitempty"`
	MaxTokens     int                  `json:"max_tokens"`
}

type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiToolFunc `json:"function"`
}

type openaiToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiChunk struct {
	Choices []struct {
		Delta struct {
			Content   string            `json:"content"`
			ToolCalls []openaiToolDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openaiToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toOpenAI translates the transcript to Chat Completions format.
func toOpenAI(system string, messages []Message) []openaiMessage {
	out := make([]openaiMessage, 0, len(messages)+1)
	if system != "" {
		out = append(out, openaiMessage{Role: "system", Content: system})
	}
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			out = append(out, openaiMessage{Role: "user", Content: m.Text()})
		case RoleAssistant:
			om := openaiMessage{Role: "assistant", Content: m.Text()}
			for _, b := range m.Blocks {
				if b.Kind == BlockToolCall {
					call := openaiToolCall{ID: b.ID, Type: "function"}
					call.Function.Name = b.Name
					call.Function.Arguments = b.Arguments
					om.ToolCalls = append(om.ToolCalls, call)
				}
			}
			out = append(out, om)
		case RoleToolResult:
			for _, b := range m.Blocks {
				if b.Kind == BlockToolResult {
					out = append(out, openaiMessage{
						Role: "tool", ToolCallID: b.ToolCallID, Content: b.Text,
					})
				}
			}
		}
	}
	return out
}

func toOpenAITools(tools []ToolDeclaration) []openaiTool {
	out := make([]openaiTool, len(tools))
	for i, t := range tools {
		out[i] = openaiTool{Type: "function", Function: openaiToolFunc{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		}}
	}
	return out
}
