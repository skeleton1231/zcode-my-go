// Package agent is the coding agent loop: it drives the provider, executes
// tool calls, owns the transcript, and reports everything as a typed event
// stream. Ported from pi's agent-loop design: fail truncated tool calls,
// encode provider failures as stop reasons, keep the transcript the only
// source of truth.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// DefaultMaxTurns caps tool-calling turns per user message, so a looping
// model cannot run forever.
const DefaultMaxTurns = 50

// retry limits for transient provider failures (ZCode's adapter retry):
// at most DefaultMaxRetries backs-off turns, the provider's Retry-After
// overriding the exponential fallback, capped so a rude header cannot
// stall the run.
const (
	DefaultMaxRetries = 3
	retryBackoffBase  = 500 * time.Millisecond
	retryBackoffCap   = 30 * time.Second
)

// ToolExecutionMode controls how a batch of tool calls runs (pi's
// ToolExecutionMode): sequential runs each call to completion before the
// next; parallel lets concurrent-safe tools overlap — end events fire in
// completion order, result blocks still assemble in source order.
type ToolExecutionMode string

// Tool execution modes.
const (
	SequentialExecution ToolExecutionMode = "sequential"
	ParallelExecution   ToolExecutionMode = "parallel"
)

// LoopConfig wires one run of the loop.
type LoopConfig struct {
	Provider  ai.Provider
	Model     string
	System    string
	Tools     []Tool
	MaxTokens int
	MaxTurns  int // <=0 means DefaultMaxTurns
	// Thinking selects the reasoning effort handed to the provider.
	Thinking ai.ThinkingLevel
	// Hooks observe and shape every tool call (pi's beforeToolCall /
	// afterToolCall): before runs after the permission gate and may block
	// or terminate the run; after may rewrite the result before it lands.
	Hooks Hooks
	// ToolExecution picks the batch mode; empty means sequential. A batch
	// containing any serial-only tool (ask, todo) always runs sequentially,
	// whatever this says.
	ToolExecution ToolExecutionMode
	// History returns the transcript to send; called once per turn so
	// steering messages queued between turns are picked up.
	History func() []ai.Message
	// Steering drains user messages queued while the run is in flight
	// (pi's getSteeringMessages, queue mode "all"): each turn start
	// injects them into the transcript, and a queued message keeps the
	// loop running past an end_turn. Nil means no steering.
	Steering func() []ai.Message
	// SessionID labels AskRequests with the run's session.
	SessionID string
	// RetryBackoff overrides the exponential base for transient-failure
	// retries (tests inject a millisecond; 0 uses the default).
	RetryBackoff time.Duration

	// Permissions gates tool execution (ZCode's permission service). Nil
	// bypasses checking entirely — the v0.2 behavior — for callers that
	// have not adopted the system yet.
	Permissions *permission.Service
	// Ask resolves an "ask" decision with the user; it blocks the loop
	// until answered. Required when Permissions yields Ask; without it an
	// ask is a denial on the safe side.
	Ask func(AskRequest) AskResponse
}

// Loop runs agent turns over a transcript sink.
type Loop struct {
	cfg  LoopConfig
	emit func(Event)
	// askMu 串行化权限确认：并行批次的 gate 可能同时要求用户确认，
	// 确认通道（桌面 modal、终端 stdin）都是单实例，并发会互相覆盖
	// 或把答案错配给另一个请求。
	askMu sync.Mutex

	terminateOnce sync.Once
	terminated    bool
	terminateMsg  string
}

// setTerminate records a hook's request to end the run after the batch.
func (l *Loop) setTerminate(reason string) {
	l.terminateOnce.Do(func() {
		l.terminated = true
		l.terminateMsg = reason
	})
}

// NewLoop builds a loop that reports through emit.
func NewLoop(cfg LoopConfig, emit func(Event)) *Loop {
	return &Loop{cfg: cfg, emit: emit}
}

// Run drives turns until the model stops calling tools, fails, is aborted,
// or the turn cap is hit. The transcript sink must be the session's append;
// the loop never rewrites history.
func (l *Loop) Run(ctx context.Context, appendMessage func(ai.Message) error) ai.StopReason {
	l.emit(Event{Kind: EvAgentStart})
	stop := runInner(ctx, l, appendMessage)
	l.emit(Event{Kind: EvAgentEnd, StopReason: stop.stop, Usage: stop.usage,
		ErrorMessage: stop.err})
	return stop.stop
}

type stopInfo struct {
	stop  ai.StopReason
	usage ai.Usage
	err   string
}

func runInner(ctx context.Context, l *Loop, appendMessage func(ai.Message) error) stopInfo {
	maxTurns := l.cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	var total ai.Usage
	for turn := 0; ; turn++ {
		if turn >= maxTurns {
			return stopInfo{stop: ai.StopEndTurn, usage: total,
				err: "reached the turn limit; stopping for a new instruction"}
		}
		// Drain steering queued while the run is in flight: the messages
		// enter the transcript silently — the surface already showed them
		// optimistically when the user hit send (pi emits message events;
		// our UI folds both views onto the same history).
		if l.cfg.Steering != nil {
			for _, m := range l.cfg.Steering() {
				if err := appendMessage(m); err != nil {
					return stopInfo{stop: ai.StopError, usage: total, err: err.Error()}
				}
			}
		}
		l.emit(Event{Kind: EvTurnStart, TurnIndex: turn})

		msg, end := l.streamAssistantWithRetry(ctx, turn)
		if err := appendMessage(msg); err != nil {
			return stopInfo{stop: ai.StopError, usage: total, err: err.Error()}
		}
		total = total.Add(end.usage)
		l.emit(Event{Kind: EvMessageEnd, StopReason: end.stop,
			ErrorMessage: end.err, Usage: total, TurnIndex: turn})

		calls := msg.ToolCalls()
		switch {
		case end.stop == ai.StopError || end.stop == ai.StopAborted:
			l.emit(Event{Kind: EvTurnEnd, TurnIndex: turn})
			return stopInfo{stop: end.stop, usage: total, err: end.err}
		case len(calls) == 0:
			l.emit(Event{Kind: EvTurnEnd, TurnIndex: turn})
			// pi's inner-loop condition: end_turn only ends the run when
			// nothing was queued meanwhile — a queued message enters the
			// transcript here and keeps the loop going.
			if l.cfg.Steering != nil {
				if queued := l.cfg.Steering(); len(queued) > 0 {
					for _, m := range queued {
						if err := appendMessage(m); err != nil {
							return stopInfo{stop: ai.StopError, usage: total, err: err.Error()}
						}
					}
					continue
				}
			}
			return stopInfo{stop: ai.StopEndTurn, usage: total}
		}

		// A "length" stop means the output was cut off by the token limit, so
		// every tool call in the message may carry truncated arguments: fail
		// them all instead of executing potentially borked calls (pi's
		// failToolCallsFromTruncatedMessage).
		results := l.executeBatch(ctx, turn, msg, calls, end.stop)
		if err := appendMessage(ai.Message{Role: ai.RoleToolResult, Blocks: results}); err != nil {
			return stopInfo{stop: ai.StopError, usage: total, err: err.Error()}
		}
		l.emit(Event{Kind: EvTurnEnd, TurnIndex: turn})
		if l.terminated { // hook 请求终止：批次结果已入账，运行到此为止
			return stopInfo{stop: ai.StopEndTurn, usage: total, err: l.terminateMsg}
		}
	}
}

// streamAssistantWithRetry retries transient provider failures (429, 5xx,
// overloaded): the failed turn never enters the transcript; each retry is
// announced as EvTurnRetry so the UI can drop the partial output.
func (l *Loop) streamAssistantWithRetry(ctx context.Context, turn int) (ai.Message, endInfo) {
	msg, end := l.streamAssistant(ctx, turn)
	for retries := 1; end.stop == ai.StopError && end.retryable && retries <= DefaultMaxRetries; retries++ {
		base := l.cfg.RetryBackoff
		if base <= 0 {
			base = retryBackoffBase
		}
		wait := retryWait(end.retryAfterMS, base, retries)
		l.emit(Event{Kind: EvTurnRetry, TurnIndex: turn,
			Text: fmt.Sprintf("第 %d 次重试（%s）：%s", retries, wait.Round(time.Millisecond), end.err)})
		select {
		case <-ctx.Done():
			return msg, endInfo{stop: ai.StopAborted}
		case <-time.After(wait):
		}
		msg, end = l.streamAssistant(ctx, turn)
	}
	return msg, end
}

// serverRetryCeiling 是服务端明确指示等待的独立宽上限（issue #59）：
// 重度限流的 Retry-After 常见 60~120s，本地 30s cap 截短会让每次重试
// 都提前砸在限流窗口内，把「稍等片刻」恶化成必然烧完重试预算。
const serverRetryCeiling = 5 * time.Minute

// retryWait 计算下一次重试的等待：服务端指示（Retry-After）用宽上限，
// 否则本地指数退避用 30s cap。纯函数便于测试。
func retryWait(retryAfterMS int, base time.Duration, retries int) time.Duration {
	if retryAfterMS > 0 {
		wait := time.Duration(retryAfterMS) * time.Millisecond
		if wait > serverRetryCeiling {
			wait = serverRetryCeiling
		}
		return wait
	}
	wait := base << retries
	if wait > retryBackoffCap {
		wait = retryBackoffCap
	}
	return wait
}

// execute runs one tool call and reports its outcome through events. The
// permission gate runs first: a denial never reaches the tool.
func (l *Loop) execute(ctx context.Context, turn int, msg ai.Message, call ai.Block) ToolResult {
	l.emit(Event{Kind: EvToolExecutionStart, ToolCallID: call.ID, ToolName: call.Name,
		Args: call.Arguments, TurnIndex: turn})

	var result ToolResult
	tool, ok := l.tool(call.Name)
	if !ok {
		result = ToolResult{IsError: true, Content: "error: unknown tool " + call.Name}
	} else if blocked := l.gate(call, tool); blocked != nil {
		result = *blocked
	} else if blocked := l.beforeHook(msg, call); blocked != nil {
		result = *blocked
	} else {
		args := strings.TrimSpace(call.Arguments)
		if args == "" {
			args = "{}"
		}
		result = tool.Execute(ctx, []byte(args))
		if result.Content == "" && !result.IsError {
			result.Content = "(no output)"
		}
		result = l.afterHook(msg, call, result)
	}

	l.emit(Event{Kind: EvToolExecutionEnd, ToolCallID: call.ID, ToolName: call.Name,
		IsError: result.IsError, ResultContent: result.Content, Diff: result.Diff, TurnIndex: turn})
	return result
}

// executeBatch runs one turn's tool calls and returns the result blocks
// in source order (pi's executor: sequential by default; parallel lets
// concurrent-safe tools overlap, end events in completion order).
func (l *Loop) executeBatch(ctx context.Context, turn int, msg ai.Message, calls []ai.Block, stop ai.StopReason) []ai.Block {
	if l.cfg.ToolExecution == ParallelExecution && batchIsParallelizable(l.cfg.Tools, calls) {
		return l.executeBatchParallel(ctx, turn, msg, calls, stop)
	}
	blocks := make([]ai.Block, 0, len(calls))
	for _, call := range calls {
		blocks = append(blocks, l.executeOne(ctx, turn, msg, call, stop))
	}
	return blocks
}

// executeOne runs a single call, wrapping its outcome as a result block.
func (l *Loop) executeOne(ctx context.Context, turn int, msg ai.Message, call ai.Block, stop ai.StopReason) ai.Block {
	var result ToolResult
	if stop == ai.StopLength {
		result = ToolResult{IsError: true, Content: "error: the response hit the token " +
			"limit before the tool arguments finished; re-issue the call in full"}
	} else {
		result = l.execute(ctx, turn, msg, call)
	}
	return ai.Block{Kind: ai.BlockToolResult, ToolCallID: call.ID,
		Text: result.Content, IsError: result.IsError, Diff: result.Diff}
}

// executeBatchParallel overlaps the batch's execution: each tool emits its
// own end event when it finishes (completion order, as pi documents),
// while the blocks assemble by source order for the transcript. 同路径的
// 读-改-写调用（write/edit/patch）组内顺序执行（issue #78：并行 RMW
// 竞态曾让同文件多 edit 静默丢改动，10/10 复现），组间保持并行。
func (l *Loop) executeBatchParallel(ctx context.Context, turn int, msg ai.Message, calls []ai.Block, stop ai.StopReason) []ai.Block {
	blocks := make([]ai.Block, len(calls))
	var wg sync.WaitGroup
	for _, group := range l.pathGroups(calls) {
		wg.Add(1)
		go func(group []int) {
			defer wg.Done()
			for _, i := range group {
				blocks[i] = l.executeOne(ctx, turn, msg, calls[i], stop)
			}
		}(group)
	}
	wg.Wait()
	return blocks
}

// pathGroups 把批次按下标分组：同 PathKey 的调用同组（顺序执行）；
// 无键的工具各自成组；PathGroupable 工具解析不出路径的保守归入
// 串行组（与任何同批调用都不并行）。
func (l *Loop) pathGroups(calls []ai.Block) [][]int {
	var serial []int // 保守串行组（解析失败的 RMW 调用）
	byPath := map[string][]int{}
	singles := [][]int{}
	for i, call := range calls {
		key, grouped := "", false
		for _, t := range l.cfg.Tools {
			if t.Name() != call.Name {
				continue
			}
			if p, ok := t.(PathGroupable); ok {
				if path, ok := p.PathKey(call.Arguments); ok && path != "" {
					key = "path:" + path
					grouped = true
				} else {
					// 声称按路径分组却解析失败：保守串行。
					key = "serial"
					grouped = true
				}
			}
			break
		}
		switch {
		case key == "serial":
			serial = append(serial, i)
		case grouped:
			byPath[key] = append(byPath[key], i)
		default:
			singles = append(singles, []int{i})
		}
	}
	groups := singles
	for _, idxs := range byPath {
		groups = append(groups, idxs)
	}
	if len(serial) > 0 {
		groups = append(groups, serial)
	}
	return groups
}

// batchIsParallelizable reports whether every call's tool may overlap.
func batchIsParallelizable(tools []Tool, calls []ai.Block) bool {
	for _, call := range calls {
		for _, t := range tools {
			if t.Name() == call.Name {
				if s, ok := t.(SerialOnly); ok && s.SerialOnly() {
					return false
				}
				break
			}
		}
	}
	return true
}

// beforeHook runs the BeforeToolCall hook; a non-nil result is the
// blocked outcome. Terminate asks the run to stop after this batch.
func (l *Loop) beforeHook(msg ai.Message, call ai.Block) *ToolResult {
	if l.cfg.Hooks.BeforeToolCall == nil {
		return nil
	}
	args := strings.TrimSpace(call.Arguments)
	if args == "" {
		args = "{}"
	}
	res := l.cfg.Hooks.BeforeToolCall(BeforeToolCallContext{
		AssistantMessage: msg, ToolCall: call, Args: json.RawMessage(args)})
	if res.Terminate {
		l.setTerminate(res.Reason)
	}
	if !res.Block {
		return nil
	}
	reason := res.Reason
	if reason == "" {
		reason = "blocked by preToolUse hook"
	}
	return &ToolResult{IsError: true,
		Content: "error: hook blocked the call — " + reason}
}

// afterHook runs the AfterToolCall hook and applies its rewrite.
func (l *Loop) afterHook(msg ai.Message, call ai.Block, result ToolResult) ToolResult {
	if l.cfg.Hooks.AfterToolCall == nil {
		return result
	}
	res := l.cfg.Hooks.AfterToolCall(AfterToolCallContext{
		AssistantMessage: msg, ToolCall: call, Result: result})
	if !res.Changed {
		return result
	}
	return ToolResult{Content: res.Content, IsError: res.IsError}
}

// gate checks the permission system for a call. A nil result means the call
// may run; a non-nil one is the blocked outcome to report.
func (l *Loop) gate(call ai.Block, tool Tool) *ToolResult {
	if l.cfg.Permissions == nil {
		return nil // 未启用权限系统：完全旁路（v0.2 行为）
	}
	argsLine := SummarizeArgs(nilIfEmpty(call.Arguments))
	decision := l.cfg.Permissions.Check(
		permission.Context{Tool: call.Name, Args: argsLine},
		capabilityFor(tool, string(call.Arguments)))
	switch decision.Behavior {
	case permission.Allow:
		return nil
	case permission.Deny:
		return blocked("denied by rule "+decision.RuleID+"（"+decision.Reason+"）",
			"换一种做法，或请用户调整权限规则")
	case permission.Ask:
		if l.cfg.Ask == nil {
			return blocked("no approval channel", "当前没有确认通道，保守起见拒绝执行")
		}
		preview := ""
		if pd, ok := tool.(PreviewDiffer); ok {
			preview = pd.PreviewDiff([]byte(call.Arguments)) // 不落盘：纯预览
		}
		l.askMu.Lock()
		resp := l.cfg.Ask(AskRequest{
			SessionID: l.cfg.SessionID, Tool: call.Name, Args: argsLine,
			Reason: decision.Reason, Risk: decision.Risk, Diff: preview})
		l.askMu.Unlock()
		if !resp.Deny {
			if resp.Always {
				l.cfg.Permissions.GrantSession(
					permission.RuleForTool(call.Name, argsLine))
			}
			return nil
		}
		note := resp.Note
		if note == "" {
			note = "请换一种做法，或说明你需要这次操作的理由"
		}
		return blocked("用户拒绝了这次调用", note)
	}
	return nil
}

func blocked(reason, guidance string) *ToolResult {
	return &ToolResult{IsError: true, Content: "error: permission " + reason +
		"；" + guidance}
}

func nilIfEmpty(args string) []byte {
	if strings.TrimSpace(args) == "" {
		return nil
	}
	return []byte(args)
}

func (l *Loop) tool(name string) (Tool, bool) {
	for _, t := range l.cfg.Tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// streamAssistant consumes one provider stream into a complete assistant
// message, forwarding deltas as events.
func (l *Loop) streamAssistant(ctx context.Context, turn int) (ai.Message, endInfo) {
	var text strings.Builder
	var think strings.Builder
	var signature string
	type callBuilder struct {
		id, name string
		args     strings.Builder
	}
	var calls []callBuilder
	var end endInfo

	l.emit(Event{Kind: EvMessageStart, TurnIndex: turn})
	for ev := range l.cfg.Provider.Stream(ctx, ai.Request{
		Model:     l.cfg.Model,
		System:    l.cfg.System,
		Messages:  l.cfg.History(),
		Tools:     declarations(l.cfg.Tools),
		MaxTokens: l.cfg.MaxTokens,
		Thinking:  l.cfg.Thinking,
	}) {
		switch ev.Kind {
		case ai.EvTextDelta:
			text.WriteString(ev.TextDelta)
			l.emit(Event{Kind: EvTextDelta, Text: ev.TextDelta, TurnIndex: turn})
		case ai.EvThinkingDelta:
			if ev.ThinkingText != "" {
				think.WriteString(ev.ThinkingText)
				l.emit(Event{Kind: EvThinkingDelta, Text: ev.ThinkingText, TurnIndex: turn})
			}
			if ev.Signature != "" {
				signature += ev.Signature
			}
		case ai.EvToolCallStart:
			calls = append(calls, callBuilder{id: ev.ToolCallID, name: ev.ToolCallName})
			l.emit(Event{Kind: EvToolCallStart, ToolCallID: ev.ToolCallID,
				ToolName: ev.ToolCallName, TurnIndex: turn})
		case ai.EvToolCallDelta:
			if len(calls) > 0 {
				// 按调用 ID 装配（issue #28）：并行调用的参数分片按 index
				// 交错下发，「粘到最后一个 Start」会让 call_A 参数全丢、
				// call_B 拿到交错垃圾 JSON。无 ID 的 provider 回退最后
				// 一个调用（顺序流安全）。
				c := &calls[len(calls)-1]
				if ev.ToolCallID != "" {
					for j := range calls {
						if calls[j].id == ev.ToolCallID {
							c = &calls[j]
							break
						}
					}
				}
				c.args.WriteString(ev.ToolCallArgs)
				l.emit(Event{Kind: EvToolCallDelta, ToolCallID: c.id,
					ArgsDelta: ev.ToolCallArgs, TurnIndex: turn})
			}
		case ai.EvMessageEnd:
			end = endInfo{stop: ev.StopReason, usage: ev.Usage, err: ev.ErrorMessage,
				retryable: ev.Retryable, retryAfterMS: ev.RetryAfterMS}
		}
	}
	if end.stop == "" {
		// The provider contract guarantees a final message_end; treat its
		// absence as a provider defect rather than trusting partial output.
		end = endInfo{stop: ai.StopError, err: "provider stream ended without a stop reason"}
	}

	msg := ai.Message{Role: ai.RoleAssistant}
	if th := think.String(); th != "" {
		msg.Blocks = append(msg.Blocks, ai.Block{Kind: ai.BlockThinking,
			Text: th, Signature: signature})
	}
	if t := text.String(); t != "" {
		msg.Blocks = append(msg.Blocks, ai.Block{Kind: ai.BlockText, Text: t})
	}
	for _, c := range calls {
		msg.Blocks = append(msg.Blocks, ai.Block{Kind: ai.BlockToolCall,
			ID: c.id, Name: c.name, Arguments: c.args.String()})
	}
	if end.stop == ai.StopToolUse && len(calls) == 0 {
		end.stop = ai.StopEndTurn // a tool_use stop without calls is just an end
	}
	return msg, end
}

// endInfo carries how a provider turn ended.
type endInfo struct {
	stop         ai.StopReason
	usage        ai.Usage
	err          string
	retryable    bool
	retryAfterMS int
}
