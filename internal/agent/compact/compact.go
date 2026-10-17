// Package compact is the context compaction system, translated from
// ZCode's core/src/compact (policy.ts, manual.ts, prompt.ts): a token
// budget with an output reserve and buffer, a decision function with a
// circuit breaker, and the summary prompt that replaces old history.
package compact

import (
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// Policy constants from ZCode's compact/policy.ts. The provider's context
// window is shared by input and output, so auto-compact can only spend the
// input side: the threshold's denominator subtracts the output reserve
// before the buffer.
const (
	DefaultContextWindow         = 200_000
	PreflightOutputReserveTokens = 21_000
	BufferTokens                 = 13_000
	DefaultThresholdPercent      = 100
	MaxConsecutiveFailures       = 3
	DefaultKeepTail              = 6 // messages kept verbatim after a compaction
)

// Policy configures auto-compaction.
type Policy struct {
	Enabled                bool
	ContextWindow          int
	OutputReserveTokens    int
	BufferTokens           int
	ThresholdPercent       int
	MaxConsecutiveFailures int
}

// DefaultPolicy is ZCode's default wiring.
func DefaultPolicy() Policy {
	return Policy{
		Enabled:                true,
		ContextWindow:          DefaultContextWindow,
		OutputReserveTokens:    PreflightOutputReserveTokens,
		BufferTokens:           BufferTokens,
		ThresholdPercent:       DefaultThresholdPercent,
		MaxConsecutiveFailures: MaxConsecutiveFailures,
	}
}

// Threshold is the token count at which compaction triggers.
func (p Policy) Threshold() int {
	window := p.ContextWindow
	if window <= 0 {
		window = DefaultContextWindow
	}
	reserve := p.OutputReserveTokens
	if reserve <= 0 || reserve > PreflightOutputReserveTokens {
		reserve = PreflightOutputReserveTokens
	}
	buffer := p.BufferTokens
	if buffer <= 0 {
		buffer = BufferTokens
	}
	pct := p.ThresholdPercent
	if pct <= 0 {
		pct = DefaultThresholdPercent
	}
	effective := window - reserve - buffer
	if effective < 0 {
		effective = 0
	}
	return effective * pct / 100
}

// Reason a decision came to, from ZCode's AutoCompactDecision.reason.
type Reason string

// Decision reasons.
const (
	ReasonDisabled       Reason = "disabled"
	ReasonNotEnough      Reason = "not_enough_messages"
	ReasonCircuitBreaker Reason = "circuit_breaker"
	ReasonBelowThreshold Reason = "below_threshold"
	ReasonAboveThreshold Reason = "above_threshold"
)

// Decision is whether to compact now.
type Decision struct {
	ShouldCompact bool
	TokenEstimate int
	Threshold     int
	Reason        Reason
}

// Decide applies the policy to the current state.
func Decide(p Policy, tokenEstimate, messageCount, consecutiveFailures int) Decision {
	threshold := p.Threshold()
	switch {
	case !p.Enabled:
		return Decision{Reason: ReasonDisabled, Threshold: threshold}
	case messageCount < 2:
		return Decision{Reason: ReasonNotEnough, Threshold: threshold}
	case consecutiveFailures >= maxFailures(p):
		return Decision{Reason: ReasonCircuitBreaker, Threshold: threshold}
	case tokenEstimate >= threshold:
		return Decision{ShouldCompact: true, TokenEstimate: tokenEstimate,
			Threshold: threshold, Reason: ReasonAboveThreshold}
	default:
		return Decision{TokenEstimate: tokenEstimate,
			Threshold: threshold, Reason: ReasonBelowThreshold}
	}
}

func maxFailures(p Policy) int {
	if p.MaxConsecutiveFailures > 0 {
		return p.MaxConsecutiveFailures
	}
	return MaxConsecutiveFailures
}

// EstimateTokens sizes a transcript. ZCode estimates from message content;
// per-message overhead plus content tokens. CJK 内容按每字 1 token 计
// （主流 tokenizer 对中文约 1~1.5 字/token），其他按 0.4 token/字——
// 旧的统一 0.4 对中文低估约 2 倍，auto-compact 触发过晚，压缩前就可能
// 撞 provider 上下文上限（issue #19）。
func EstimateTokens(messages []ai.Message) int {
	tokens := 0
	for _, m := range messages {
		for _, b := range m.Blocks {
			tokens += estimateTextTokens(b.Text)
			tokens += estimateTextTokens(b.Arguments)
		}
		tokens += 16 // role and framing overhead
	}
	return tokens
}

// estimateTextTokens 按 rune 自适应：CJK（> U+2E80，含汉字、日文假名、
// 韩文）每字 1 token，其他每字 2/5 token。
func estimateTextTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if r > 0x2E80 {
			cjk++
		} else {
			other++
		}
	}
	return cjk + other*2/5
}

// SummaryUserPrompt asks the model for the structured summary that
// replaces old history (ZCode's compact prompt: goals, decisions, state,
// next steps — with tool results compressed to their conclusions).
func SummaryUserPrompt() string {
	return "请把以上对话压缩成一份结构化摘要，供继续同一任务使用。用第二人称保留可执行的状态，包含以下小节：\n" +
		"1. 任务目标：用户要什么（原话要点）\n" +
		"2. 已完成：做过什么、改过哪些文件（路径 + 一句话）\n" +
		"3. 关键决策与约束：用户确认过的偏好、路径、技术选型\n" +
		"4. 当前状态：进行到哪一步、正在等什么\n" +
		"5. 下一步：接下来该做的具体动作\n" +
		"工具调用的细节压缩成结论（成功/失败与关键输出），不要复述完整输出。直接输出摘要正文。"
}

// SummaryMessage wraps a summary as the user message that re-opens the
// context after compaction.
func SummaryMessage(summary string) ai.Message {
	return ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{
		Kind: ai.BlockText,
		Text: "（会话已压缩；以下是此前对话的摘要）\n\n" + strings.TrimSpace(summary),
	}}}
}

// PruneCapBytes is the size above which an old tool result is pruned
// (ZCode's microcompact keeps recent output intact and stubs the rest).
const PruneCapBytes = 512

// Microcompact prunes tool outputs outside the keep window to stubs —
// ZCode's microcompact: cheaper than a full summary, no provider needed,
// and the structure of the conversation stays. The messages are copied;
// the report says whether anything changed.
func Microcompact(messages []ai.Message, keepWindow int) ([]ai.Message, bool) {
	if keepWindow < 0 {
		keepWindow = 0
	}
	out := append([]ai.Message(nil), messages...)
	changed := false
	cutoff := len(out) - keepWindow
	for i := 0; i < cutoff; i++ {
		for j, b := range out[i].Blocks {
			if b.Kind != ai.BlockToolResult || len(b.Text) <= PruneCapBytes {
				continue
			}
			// 消息与块都是浅拷贝共享的，替换前先复制这一条。
			blocks := append([]ai.Block(nil), out[i].Blocks...)
			stub := fmt.Sprintf("[工具输出已修剪：原 %d 字节，超出保留窗口]", len([]rune(b.Text)))
			blocks[j].Text = stub
			out[i].Blocks = blocks
			changed = true
		}
	}
	return out, changed
}
