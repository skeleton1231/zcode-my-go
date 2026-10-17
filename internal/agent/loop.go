// Package agent is the coding agent loop: it drives the provider, executes
// tool calls, owns the transcript, and reports everything as a typed event
// stream. Ported from pi's agent-loop design: fail truncated tool calls,
// encode provider failures as stop reasons, keep the transcript the only
// source of truth.
package agent

import (
	"context"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// DefaultMaxTurns caps tool-calling turns per user message, so a looping
// model cannot run forever.
const DefaultMaxTurns = 50

// LoopConfig wires one run of the loop.
type LoopConfig struct {
	Provider  ai.Provider
	Model     string
	System    string
	Tools     []Tool
	MaxTokens int
	MaxTurns  int // <=0 means DefaultMaxTurns
	// History returns the transcript to send; called once per turn so
	// steering messages queued between turns are picked up.
	History func() []ai.Message
	// SessionID labels AskRequests with the run's session.
	SessionID string

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
		l.emit(Event{Kind: EvTurnStart, TurnIndex: turn})

		msg, end := l.streamAssistant(ctx, turn)
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
			return stopInfo{stop: ai.StopEndTurn, usage: total}
		}

		// A "length" stop means the output was cut off by the token limit, so
		// every tool call in the message may carry truncated arguments: fail
		// them all instead of executing potentially borked calls (pi's
		// failToolCallsFromTruncatedMessage).
		results := make([]ai.Block, 0, len(calls))
		for _, call := range calls {
			var result ToolResult
			if end.stop == ai.StopLength {
				result = ToolResult{IsError: true, Content: "error: the response hit the token " +
					"limit before the tool arguments finished; re-issue the call in full"}
			} else {
				result = l.execute(ctx, turn, call)
			}
			results = append(results, ai.Block{
				Kind: ai.BlockToolResult, ToolCallID: call.ID,
				Text: result.Content, IsError: result.IsError})
		}
		if err := appendMessage(ai.Message{Role: ai.RoleToolResult, Blocks: results}); err != nil {
			return stopInfo{stop: ai.StopError, usage: total, err: err.Error()}
		}
		l.emit(Event{Kind: EvTurnEnd, TurnIndex: turn})
	}
}

// execute runs one tool call and reports its outcome through events. The
// permission gate runs first: a denial never reaches the tool.
func (l *Loop) execute(ctx context.Context, turn int, call ai.Block) ToolResult {
	l.emit(Event{Kind: EvToolExecutionStart, ToolCallID: call.ID, ToolName: call.Name,
		Args: call.Arguments, TurnIndex: turn})

	var result ToolResult
	tool, ok := l.tool(call.Name)
	if !ok {
		result = ToolResult{IsError: true, Content: "error: unknown tool " + call.Name}
	} else if blocked := l.gate(call, tool); blocked != nil {
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
	}

	l.emit(Event{Kind: EvToolExecutionEnd, ToolCallID: call.ID, ToolName: call.Name,
		IsError: result.IsError, ResultContent: result.Content, TurnIndex: turn})
	return result
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
		resp := l.cfg.Ask(AskRequest{
			SessionID: l.cfg.SessionID, Tool: call.Name, Args: argsLine,
			Reason: decision.Reason, Risk: decision.Risk})
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
	}) {
		switch ev.Kind {
		case ai.EvTextDelta:
			text.WriteString(ev.TextDelta)
			l.emit(Event{Kind: EvTextDelta, Text: ev.TextDelta, TurnIndex: turn})
		case ai.EvToolCallStart:
			calls = append(calls, callBuilder{id: ev.ToolCallID, name: ev.ToolCallName})
			l.emit(Event{Kind: EvToolCallStart, ToolCallID: ev.ToolCallID,
				ToolName: ev.ToolCallName, TurnIndex: turn})
		case ai.EvToolCallDelta:
			if len(calls) > 0 {
				c := &calls[len(calls)-1]
				c.args.WriteString(ev.ToolCallArgs)
				l.emit(Event{Kind: EvToolCallDelta, ToolCallID: c.id,
					ArgsDelta: ev.ToolCallArgs, TurnIndex: turn})
			}
		case ai.EvMessageEnd:
			end = endInfo{stop: ev.StopReason, usage: ev.Usage, err: ev.ErrorMessage}
		}
	}
	if end.stop == "" {
		// The provider contract guarantees a final message_end; treat its
		// absence as a provider defect rather than trusting partial output.
		end = endInfo{stop: ai.StopError, err: "provider stream ended without a stop reason"}
	}

	msg := ai.Message{Role: ai.RoleAssistant}
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
	stop  ai.StopReason
	usage ai.Usage
	err   string
}
