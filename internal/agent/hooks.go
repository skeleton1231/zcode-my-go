package agent

// Tool hooks (pi's beforeToolCall/afterToolCall): code that observes or
// shapes every tool call. The loop runs BeforeToolCall after the
// permission gate (a user-approved call is worth extending) and
// AfterToolCall before the result enters the transcript.

import (
	"encoding/json"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// BeforeToolCallContext describes the call about to run.
type BeforeToolCallContext struct {
	AssistantMessage ai.Message
	ToolCall         ai.Block
	// Args is the validated tool arguments object ({} on none).
	Args json.RawMessage
}

// BeforeToolCallResult shapes the call: Block prevents execution (the
// loop reports an error result carrying Reason), Terminate additionally
// ends the run after this batch (pi's terminate hint).
type BeforeToolCallResult struct {
	Block     bool
	Reason    string
	Terminate bool
}

// AfterToolCallContext describes the executed call and its result.
type AfterToolCallContext struct {
	AssistantMessage ai.Message
	ToolCall         ai.Block
	Result           ToolResult
}

// AfterToolCallResult rewrites the executed result (pi's field-by-field
// merge, simplified): Changed marks a rewrite; Content and IsError then
// replace the executed values wholesale.
type AfterToolCallResult struct {
	Content string
	IsError bool
	Changed bool
}

// Hooks bundles the loop's hook points.
type Hooks struct {
	BeforeToolCall func(BeforeToolCallContext) BeforeToolCallResult
	AfterToolCall  func(AfterToolCallContext) AfterToolCallResult
}
