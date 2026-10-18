package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
	res, _, err := RunSubtaskSeeded(ctx, cfg, nil, prompt)
	return res, err
}

// RunSubtaskResume 在既有子代理历史上续跑（spec 规则 52，ZCode
// SendMessage 的落地）：追加一条用户消息后继续嵌套循环——此前的调查、
// 工具结果与结论全部保留，子代理无需重看上下文。返回终态历史供再次
// 续跑。
func RunSubtaskResume(ctx context.Context, cfg SubtaskConfig, prior []ai.Message, message string) (SubtaskResult, []ai.Message, error) {
	return RunSubtaskSeeded(ctx, cfg, prior, message)
}

// RunSubtaskSeeded 是共用核心：prior 为空即全新子任务，非空即续跑
// （拷贝隔离——不与调用方共享底层数组）。
func RunSubtaskSeeded(ctx context.Context, cfg SubtaskConfig, prior []ai.Message, prompt string) (SubtaskResult, []ai.Message, error) {
	if strings.TrimSpace(prompt) == "" {
		return SubtaskResult{}, nil, fmt.Errorf("empty subtask prompt")
	}
	history := append([]ai.Message(nil), prior...)
	appendMessage := func(m ai.Message) error {
		history = append(history, m)
		return nil
	}
	if err := appendMessage(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}); err != nil {
		return SubtaskResult{}, history, err
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
				return SubtaskResult{Text: text, Turns: turns}, history, nil
			}
		}
	}
	if stop == ai.StopError {
		return SubtaskResult{}, history, fmt.Errorf("subtask errored")
	}
	return SubtaskResult{}, history, fmt.Errorf("subtask produced no answer")
}

// SubtaskSystem is the nested run's system prompt.
func SubtaskSystem() string {
	return "You are a research subagent. Investigate the assigned question " +
		"with the read-only tools, then answer concisely in the user's " +
		"language. Report findings and file paths; do not modify anything."
}

// MaxParallelSubtasks caps concurrency (ZCode's subagent fan-out limit).
const MaxParallelSubtasks = 4

// RunSubtasks runs read-only subagents concurrently over the prompts
// (ZCode's parallel subagents): a worker pool of MaxParallelSubtasks,
// results in prompt order; one failure fails only its own entry.
func RunSubtasks(ctx context.Context, cfg SubtaskConfig, prompts []string) []SubtaskResult {
	results := make([]SubtaskResult, len(prompts))
	sem := make(chan struct{}, MaxParallelSubtasks)
	var wg sync.WaitGroup
	for i, prompt := range prompts {
		wg.Add(1)
		go func(i int, prompt string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 每个子代理独立转录：共享 cfg 是只读的，provider 逐流实例化。
			res, err := RunSubtask(ctx, cfg, prompt)
			if err != nil {
				results[i] = SubtaskResult{Text: "error: " + err.Error(), Turns: 0}
				return
			}
			results[i] = res
		}(i, prompt)
	}
	wg.Wait()
	return results
}
