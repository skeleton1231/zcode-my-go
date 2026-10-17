// Package uiapp is the native UI layer: the chat view state (a pure fold
// over agent events) and the window's view. Ported from the web frontend's
// reducer when the project moved to native UI.
package uiapp

import (
	"fmt"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// itemKind discriminates a chat row.
type itemKind uint8

const (
	itemUser itemKind = iota
	itemAssistant
	itemTool
	itemNotice
)

// chatItem is one row of the chat view: a user or assistant message, or a
// tool call with its result.
type chatItem struct {
	kind itemKind

	// message
	text      string
	streaming bool
	errMsg    string

	// tool
	callID  string
	name    string
	args    string
	result  string
	isError bool
	running bool
	open    bool // the collapsible card's state
}

// chatState is the fold of one session's event stream.
type chatState struct {
	items    []chatItem
	running  bool
	usage    ai.Usage
	hasUsage bool
	endErr   string
}

// apply folds one event into the state.
func (s *chatState) apply(ev agent.Event) {
	switch ev.Kind {
	case agent.EvAgentStart:
		s.running = true
		s.endErr = ""

	case agent.EvCompaction:
		s.items = append(s.items, chatItem{kind: itemNotice, text: ev.Text})

	case agent.EvTurnRetry:
		// 重试：丢弃残缺的流式输出，notice 说明原因；新回合会重新开始。
		if n := len(s.items); n > 0 && s.items[n-1].kind == itemAssistant && s.items[n-1].streaming {
			s.items = s.items[:n-1]
		}
		s.items = append(s.items, chatItem{kind: itemNotice, text: "重试：" + ev.Text})

	case agent.EvMessageStart:
		s.items = append(s.items, chatItem{kind: itemAssistant, streaming: true})

	case agent.EvTextDelta:
		for i := len(s.items) - 1; i >= 0; i-- {
			if s.items[i].kind == itemAssistant && s.items[i].streaming {
				s.items[i].text += ev.Text
				break
			}
		}

	case agent.EvToolExecutionStart:
		s.items = append(s.items, chatItem{
			kind: itemTool, callID: ev.ToolCallID, name: ev.ToolName,
			args: ev.Args, running: true,
		})

	case agent.EvToolExecutionEnd:
		for i := len(s.items) - 1; i >= 0; i-- {
			if s.items[i].kind == itemTool && s.items[i].callID == ev.ToolCallID {
				s.items[i].result = ev.ResultContent
				s.items[i].isError = ev.IsError
				s.items[i].running = false
				break
			}
		}

	case agent.EvMessageEnd:
		for i := len(s.items) - 1; i >= 0; i-- {
			if s.items[i].kind == itemAssistant && s.items[i].streaming {
				s.items[i].streaming = false
				if ev.StopReason == ai.StopError {
					s.items[i].errMsg = ev.ErrorMessage
				}
				break
			}
		}
		if ev.Usage != (ai.Usage{}) {
			s.usage = ev.Usage
			s.hasUsage = true
		}

	case agent.EvAgentEnd:
		for i := range s.items {
			s.items[i].streaming = false
			s.items[i].running = false
		}
		s.running = false
		if ev.Usage != (ai.Usage{}) {
			s.usage = ev.Usage
			s.hasUsage = true
		}
		if ev.StopReason == ai.StopError {
			s.endErr = ev.ErrorMessage
		}
	}
}

// appendUser adds the user's message before the run starts.
func (s *chatState) appendUser(text string) {
	s.items = append(s.items, chatItem{kind: itemUser, text: text})
}

// restore rebuilds view state from a transcript.
func restoreChat(messages []ai.Message) chatState {
	var s chatState
	for _, m := range messages {
		switch m.Role {
		case ai.RoleUser:
			s.items = append(s.items, chatItem{kind: itemUser, text: m.Text()})
		case ai.RoleAssistant:
			for _, b := range m.Blocks {
				switch b.Kind {
				case ai.BlockText:
					if b.Text != "" {
						s.items = append(s.items, chatItem{kind: itemAssistant, text: b.Text})
					}
				case ai.BlockToolCall:
					// 结果由后续 toolResult 消息补齐；缺失即为截断的调用。
					s.items = append(s.items, chatItem{
						kind: itemTool, callID: b.ID, name: b.Name,
						args: b.Arguments, running: true,
					})
				}
			}
		case ai.RoleToolResult:
			for _, b := range m.Blocks {
				if b.Kind != ai.BlockToolResult {
					continue
				}
				for i := range s.items {
					if s.items[i].kind == itemTool && s.items[i].callID == b.ToolCallID {
						s.items[i].result = b.Text
						s.items[i].isError = b.IsError
						s.items[i].running = false
						break
					}
				}
			}
		}
	}
	return s
}

// formatUsage renders the status bar's token count.
func formatUsage(s chatState) string {
	if !s.hasUsage {
		return ""
	}
	in := s.usage.InputTokens + s.usage.CacheReadTokens + s.usage.CacheWriteTokens
	return "↑" + formatTokens(in) + " ↓" + formatTokens(s.usage.OutputTokens)
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
