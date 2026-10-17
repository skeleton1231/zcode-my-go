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

// Gemini speaks Google's generativeLanguage API over SSE, translated from
// ZCode's provider adapters. Function calls arrive as complete objects (no
// argument streaming), so each becomes one start event plus one full-args
// delta.
type Gemini struct {
	BaseURL string // e.g. https://generativelanguage.googleapis.com
	APIKey  string
	HTTP    *http.Client
}

// NewGemini returns a client with sane timeouts.
func NewGemini(baseURL, apiKey string) *Gemini {
	return &Gemini{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

// Stream implements Provider with the shared failure contract.
func (g *Gemini) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		g.stream(ctx, req, out)
	}()
	return out
}

func (g *Gemini) stream(ctx context.Context, req Request, out chan<- StreamEvent) {
	end := func(reason StopReason, message string) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason, ErrorMessage: message}
	}
	endRetryable := func(reason StopReason, message string, retryable bool, afterMS int) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason, ErrorMessage: message,
			Retryable: retryable, RetryAfterMS: afterMS}
	}
	if g.APIKey == "" {
		end(StopError, "missing API key: set it in Settings or GEMINI_API_KEY")
		return
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body, err := json.Marshal(geminiRequest{
		SystemInstruction: geminiContent{Parts: []geminiPart{{Text: req.System}}},
		Contents:          toGemini(req.Messages),
		Tools:             toGeminiTools(req.Tools),
		GenerationConfig: geminiGeneration{
			MaxOutputTokens: maxTokens,
			ThinkingConfig:  geminiThinkingBudget(req.Thinking),
		},
	})
	if err != nil {
		end(StopError, fmt.Sprintf("encoding request: %v", err))
		return
	}
	if os.Getenv("ZGO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[zgo-debug] gemini request: %s\n", body)
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", g.BaseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		end(StopError, fmt.Sprintf("building request: %v", err))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.APIKey)

	resp, err := g.HTTP.Do(httpReq)
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

	g.parseSSE(ctx, resp.Body, out)
}

func (g *Gemini) parseSSE(ctx context.Context, body io.Reader, out chan<- StreamEvent) {
	usage := Usage{}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	emitEnd := func(reason StopReason, message string) {
		out <- StreamEvent{Kind: EvMessageEnd, StopReason: reason,
			ErrorMessage: message, Usage: usage}
	}
	callSeq := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk geminiChunk
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			continue
		}
		if chunk.UsageMetadata != nil {
			usage.InputTokens = chunk.UsageMetadata.PromptTokenCount
			usage.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		cand := chunk.Candidates[0]
		for _, part := range cand.Content.Parts {
			switch {
			case part.Text != "":
				out <- StreamEvent{Kind: EvTextDelta, TextDelta: part.Text}
			case part.FunctionCall.Name != "":
				callSeq++
				args, _ := json.Marshal(part.FunctionCall.Args)
				out <- StreamEvent{Kind: EvToolCallStart,
					ToolCallID:   fmt.Sprintf("call_%d", callSeq),
					ToolCallName: part.FunctionCall.Name}
				out <- StreamEvent{Kind: EvToolCallDelta, ToolCallArgs: string(args)}
			}
		}
		if cand.FinishReason != "" {
			emitEnd(mapGeminiFinish(cand.FinishReason), "")
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
	emitEnd(StopEndTurn, "")
}

func mapGeminiFinish(reason string) StopReason {
	switch strings.ToUpper(reason) {
	case "STOP":
		return StopEndTurn
	case "MAX_TOKENS":
		return StopLength
	case "SAFETY", "RECITATION":
		return StopError
	default:
		return StopEndTurn
	}
}

// --- wire types ----------------------------------------------------------

type geminiRequest struct {
	SystemInstruction geminiContent    `json:"systemInstruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiTools    `json:"tools,omitempty"`
	GenerationConfig  geminiGeneration `json:"generationConfig,omitempty"`
}

type geminiGeneration struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
	// ThinkingBudget：0 关闭思考，-1 动态；正值为固定预算（token）。
	ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

// geminiThinkingBudget maps levels; Off disables (budget 0).
func geminiThinkingBudget(level ThinkingLevel) *geminiThinkingConfig {
	switch level {
	case ThinkingLow:
		return &geminiThinkingConfig{ThinkingBudget: 2048}
	case ThinkingMedium:
		return &geminiThinkingConfig{ThinkingBudget: 8192}
	case ThinkingHigh:
		return &geminiThinkingConfig{ThinkingBudget: 24576}
	default:
		return &geminiThinkingConfig{ThinkingBudget: 0}
	}
}

type geminiTools struct {
	FunctionDeclarations []ToolDeclaration `json:"functionDeclarations"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"` // user | model
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string           `json:"text,omitempty"`
	InlineData       *geminiInline    `json:"inlineData,omitempty"`
	FunctionCall     geminiFunction   `json:"functionCall,omitempty"`
	FunctionResponse geminiFnResponse `json:"functionResponse,omitempty"`
}

// geminiInline is an inline base64 image part.
type geminiInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunction struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFnResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiChunk struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// toGemini translates the transcript; function responses pair with the
// tool names of the calls that asked for them.
func toGemini(messages []Message) []geminiContent {
	names := map[string]string{} // callID → tool name
	out := make([]geminiContent, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			parts := make([]geminiPart, 0, len(m.Blocks))
			for _, b := range m.Blocks {
				switch b.Kind {
				case BlockImage:
					parts = append(parts, geminiPart{
						InlineData: &geminiInline{MimeType: b.Media, Data: b.Data}})
				default:
					if b.Text != "" {
						parts = append(parts, geminiPart{Text: b.Text})
					}
				}
			}
			if len(parts) == 0 {
				parts = []geminiPart{{Text: m.Text()}}
			}
			out = append(out, geminiContent{Role: "user", Parts: parts})
		case RoleAssistant:
			content := geminiContent{Role: "model"}
			for _, b := range m.Blocks {
				switch b.Kind {
				case BlockText:
					if b.Text != "" {
						content.Parts = append(content.Parts, geminiPart{Text: b.Text})
					}
				case BlockToolCall:
					names[b.ID] = b.Name
					var args map[string]any
					if json.Unmarshal([]byte(b.Arguments), &args) != nil || b.Arguments == "" {
						args = map[string]any{}
					}
					content.Parts = append(content.Parts,
						geminiPart{FunctionCall: geminiFunction{Name: b.Name, Args: args}})
				}
			}
			out = append(out, content)
		case RoleToolResult:
			content := geminiContent{Role: "user"}
			for _, b := range m.Blocks {
				if b.Kind != BlockToolResult {
					continue
				}
				name := names[b.ToolCallID]
				if name == "" {
					name = "tool"
				}
				content.Parts = append(content.Parts, geminiPart{
					FunctionResponse: geminiFnResponse{
						Name:     name,
						Response: map[string]any{"output": b.Text},
					},
				})
			}
			out = append(out, content)
		}
	}
	return out
}

func toGeminiTools(tools []ToolDeclaration) []geminiTools {
	if len(tools) == 0 {
		return nil
	}
	return []geminiTools{{FunctionDeclarations: tools}}
}
