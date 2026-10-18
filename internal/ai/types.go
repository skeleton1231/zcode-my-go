// Package ai is the LLM provider boundary: the message types the agent core
// speaks, tool declarations, and a streaming Provider interface. Wire
// protocols live in provider implementations (anthropic.go); nothing above
// this package may depend on a concrete API.
package ai

import "context"

// Role of a message in the transcript.
type Role string

// Message roles. System is carried by Request.System, not by messages.
const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// BlockKind discriminates a content block. Only the fields documented on a
// kind are set for it; the agent loop assembles messages, providers
// translate them to their wire format.
type BlockKind string

// Content block kinds.
const (
	BlockText       BlockKind = "text"
	BlockImage      BlockKind = "image"
	BlockToolCall   BlockKind = "toolCall"
	BlockToolResult BlockKind = "toolResult"
	BlockThinking   BlockKind = "thinking"
)

// Block is one content block of a message.
type Block struct {
	Kind BlockKind `json:"type"`

	// Text (BlockText).
	Text string `json:"text,omitempty"`

	// BlockToolCall: the call's ID, tool name and raw JSON arguments.
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // raw JSON object, possibly partial

	// BlockToolResult: the call this answers, its output and error flag.
	ToolCallID string `json:"toolCallId,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	// Diff 携带改动类工具（edit/write/patch）的统一 diff——随转录持
	// 久化（spec 规则 34；此前只在事件流上存在，重载即丢）。不上三
	// 协议 wire（provider 无此概念）。
	Diff string `json:"diff,omitempty"`

	// BlockImage: the media type ("image/png"…) and the image bytes,
	// base64-encoded (JSONL persistence and every wire dialect take the
	// string form).
	Media string `json:"media,omitempty"`
	Data  string `json:"data,omitempty"`

	// BlockThinking: the model's reasoning text and the provider's
	// signature, which must be passed back verbatim on the next request
	// (Anthropic extended thinking with tool use).
	Signature string `json:"signature,omitempty"`
}

// Message is one entry of the transcript.
type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"content"`
}

// Text returns the concatenated text blocks of the message.
func (m Message) Text() string {
	text := ""
	for _, b := range m.Blocks {
		if b.Kind == BlockText {
			if text != "" {
				text += "\n"
			}
			text += b.Text
		}
	}
	return text
}

// ToolCalls returns the tool call blocks in order.
func (m Message) ToolCalls() []Block {
	var calls []Block
	for _, b := range m.Blocks {
		if b.Kind == BlockToolCall {
			calls = append(calls, b)
		}
	}
	return calls
}

// ToolDeclaration declares a tool to the model.
type ToolDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Usage is the token accounting of one assistant turn.
type Usage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

// Add accumulates the usage of another turn.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}

// StopReason ends an assistant turn. The loop maps it: ToolUse means execute
// tools and continue; Length means every tool call in the turn may be
// truncated and must be failed, not run; Error and Aborted end the run.
type StopReason string

// Stop reasons.
const (
	StopEndTurn StopReason = "end_turn"
	StopToolUse StopReason = "tool_use"
	StopLength  StopReason = "length"
	StopError   StopReason = "error"
	StopAborted StopReason = "aborted"
)

// Request is a complete streaming completion request.
// ThinkingLevel is the reasoning effort (ZCode's reasoningLevel): Off
// disables extended thinking; Low/Medium/High map to per-provider budgets.
type ThinkingLevel string

// Thinking levels.
const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
)

type Request struct {
	Model  string
	System string
	// SystemVolatile 是不参与缓存前缀的系统提示尾部（issue #139：git
	// dirty 计数每轮变化，混在缓存前缀里整块失效）。provider 依此把
	// system 拆成 稳定块（带断点）+ 易变块（不带）。
	SystemVolatile string
	Messages       []Message
	Tools          []ToolDeclaration
	MaxTokens      int
	// Thinking selects the reasoning effort (ZCode's reasoningLevel);
	// providers map it to their budget parameters. Empty means Off.
	Thinking ThinkingLevel
}

// EventKind discriminates a provider stream event.
type EventKind string

// Provider stream events.
const (
	EvMessageStart  EventKind = "message_start"
	EvTextDelta     EventKind = "text_delta"
	EvThinkingDelta EventKind = "thinking_delta"
	EvToolCallStart EventKind = "tool_call_start"
	EvToolCallDelta EventKind = "tool_call_delta"
	EvMessageEnd    EventKind = "message_end"
)

// StreamEvent is one event of an assistant turn's stream.
type StreamEvent struct {
	Kind EventKind `json:"type"`

	TextDelta    string `json:"textDelta,omitempty"`    // EvTextDelta
	ThinkingText string `json:"thinkingText,omitempty"` // EvThinkingDelta
	// Signature arrives with the thinking stream's signature_delta and
	// must ride back in the history's thinking block.
	Signature    string `json:"signature,omitempty"`
	ToolCallID   string `json:"toolCallId,omitempty"`   // EvToolCallStart/Delta
	ToolCallName string `json:"toolCallName,omitempty"` // EvToolCallStart
	ToolCallArgs string `json:"toolCallArgs,omitempty"` // EvToolCallDelta: partial JSON

	StopReason   StopReason `json:"stopReason,omitempty"`   // EvMessageEnd
	ErrorMessage string     `json:"errorMessage,omitempty"` // EvMessageEnd with StopError
	// EvMessageEnd with StopError: whether the failure is transient (a
	// retry may succeed) and how long the provider asked us to wait.
	Retryable    bool  `json:"retryable,omitempty"`
	RetryAfterMS int   `json:"retryAfterMs,omitempty"`
	Usage        Usage `json:"usage,omitempty"` // EvMessageStart/End
}

// Provider streams assistant turns. The contract, like pi's StreamFn: never
// return an error for model, network or runtime failures — deliver them as a
// final EvMessageEnd with StopError (or StopAborted when the context was
// canceled). The returned channel is closed after that final event.
type Provider interface {
	Stream(ctx context.Context, req Request) <-chan StreamEvent
}
