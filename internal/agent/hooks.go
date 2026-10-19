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

// PromptContext 描述一条即将进入会话的用户提示（spec 规则 96：原始
// 形态——@提及展开之前，钩子看到的就是用户敲的字）。
type PromptContext struct {
	SessionID string
	Workspace string
	Prompt    string
}

// PromptResult 搅动提示：Block 拒绝（Reason 给用户）；Replacement
// 非空则以之替换原文继续（改写型钩子——注入上下文/规范前缀等）。
type PromptResult struct {
	Block       bool
	Reason      string
	Replacement string
}

// Hooks bundles the loop's hook points.
type Hooks struct {
	BeforeToolCall func(BeforeToolCallContext) BeforeToolCallResult
	AfterToolCall  func(AfterToolCallContext) AfterToolCallResult
	// UserPromptSubmit 在用户提示进会话前运行（spec 规则 96）：见
	// 原始提示、可拦截或改写；nil 无钩子。
	UserPromptSubmit func(PromptContext) PromptResult
	// Stop 在回合自然收尾处询问（spec 规则 69）：Blocked 强制模型继续
	// 工作（原因进转录）；nil 即无钩子，收尾不受阻。
	Stop func(StopContext) StopResult
}

// StopContext describes the natural turn end as the stop hook sees it.
type StopContext struct {
	SessionID string
	Workspace string
	LastText  string // 本次运行最后一条 assistant 文本
}

// StopResult shapes the end: Blocked forces one more turn with Reason.
type StopResult struct {
	Blocked bool
	Reason  string
}
