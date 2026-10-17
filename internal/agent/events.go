package agent

import "github.com/skeleton1231/zcode-my-go/internal/ai"

// EventKind discriminates an agent event. Events are the UI protocol:
// additive changes only — new optional fields are fine, renamed or removed
// ones are breaking (see specs/agent-core.md).
type EventKind string

// Agent events, in emission order per spec.
const (
	EvAgentStart         EventKind = "agent_start"
	EvTurnStart          EventKind = "turn_start"
	EvMessageStart       EventKind = "message_start"
	EvTextDelta          EventKind = "text_delta"
	EvToolCallStart      EventKind = "tool_call_start"
	EvToolCallDelta      EventKind = "tool_call_delta"
	EvMessageEnd         EventKind = "message_end"
	EvToolExecutionStart EventKind = "tool_execution_start"
	EvToolExecutionEnd   EventKind = "tool_execution_end"
	EvTurnEnd            EventKind = "turn_end"
	EvAgentEnd           EventKind = "agent_end"
)

// Event is one streamed agent event, JSON-friendly for the mygo channel.
type Event struct {
	Kind      EventKind `json:"type"`
	SessionID string    `json:"sessionId"`

	Text string `json:"text,omitempty"` // EvTextDelta

	ToolCallID string `json:"toolCallId,omitempty"` // tool call identification
	ToolName   string `json:"toolName,omitempty"`
	Args       string `json:"args,omitempty"`      // EvToolExecutionStart: full JSON
	ArgsDelta  string `json:"argsDelta,omitempty"` // EvToolCallDelta: partial JSON

	StopReason   ai.StopReason `json:"stopReason,omitempty"`   // EvMessageEnd / EvAgentEnd
	ErrorMessage string        `json:"errorMessage,omitempty"` // with StopError

	IsError       bool     `json:"isError,omitempty"`       // EvToolExecutionEnd
	ResultContent string   `json:"resultContent,omitempty"` // EvToolExecutionEnd
	Usage         ai.Usage `json:"usage,omitempty"`         // EvMessageEnd / EvAgentEnd (totals)
	TurnIndex     int      `json:"turnIndex"`
}
