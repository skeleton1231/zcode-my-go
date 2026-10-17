package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// SubtaskConfig wires a nested agent run (ZCode's subagent): its own loop,
// an ephemeral transcript, and typically a read-only toolset.
type SubtaskConfig struct {
	Provider    ai.Provider
	Model       string
	System      string
	Tools       []Tool
	MaxTurns    int
	MaxTokens   int
	Permissions *permission.Service // edit 侧传入：工作流步骤同受权限门
	Ask         func(AskRequest) AskResponse
}

// SubtaskResult is the nested run's outcome.
type SubtaskResult struct {
	Text  string // final assistant message
	Turns int
}

// RunSubtask runs a nested agent loop to completion and returns its final
// answer. The transcript is in-memory and dies with the run; nothing is
// persisted.
func RunSubtask(ctx context.Context, cfg SubtaskConfig, prompt string) (SubtaskResult, error) {
	if strings.TrimSpace(prompt) == "" {
		return SubtaskResult{}, fmt.Errorf("empty subtask prompt")
	}
	var history []ai.Message
	appendMessage := func(m ai.Message) error {
		history = append(history, m)
		return nil
	}
	if err := appendMessage(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}); err != nil {
		return SubtaskResult{}, err
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 20
	}
	turns := 0
	loop := NewLoop(LoopConfig{
		Provider:    cfg.Provider,
		Model:       cfg.Model,
		System:      cfg.System,
		Tools:       cfg.Tools,
		MaxTokens:   cfg.MaxTokens,
		MaxTurns:    maxTurns,
		History:     func() []ai.Message { return history },
		Permissions: cfg.Permissions,
		Ask:         cfg.Ask,
	}, func(ev Event) {
		if ev.Kind == EvTurnStart {
			turns++
		}
	})
	stop := loop.Run(ctx, appendMessage)

	// 最后一条 assistant 文本即子任务结论。
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == ai.RoleAssistant {
			text := strings.TrimSpace(history[i].Text())
			if text != "" {
				return SubtaskResult{Text: text, Turns: turns}, nil
			}
		}
	}
	if stop == ai.StopError {
		return SubtaskResult{}, fmt.Errorf("subtask errored")
	}
	return SubtaskResult{}, fmt.Errorf("subtask produced no answer")
}

// SubtaskSystem is the nested run's system prompt.
func SubtaskSystem() string {
	return "You are a research subagent. Investigate the assigned question " +
		"with the read-only tools, then answer concisely in the user's " +
		"language. Report findings and file paths; do not modify anything."
}
