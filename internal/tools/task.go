package tools

import (
	"context"
	"encoding/json"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Task delegates a self-contained question to a nested read-only agent
// (ZCode's subagent tool): the main thread keeps working while the subagent
// explores.
type Task struct {
	// Run executes a subtask; the service wires it to agent.RunSubtask.
	Run func(ctx context.Context, prompt string) (agent.SubtaskResult, error)
}

// NewTask builds the task tool over a runner.
func NewTask(run func(ctx context.Context, prompt string) (agent.SubtaskResult, error)) *Task {
	return &Task{Run: run}
}

// Name implements agent.Tool.
func (t *Task) Name() string { return "task" }

// Description implements agent.Tool.
func (t *Task) Description() string {
	return "Delegate a self-contained research question to a read-only " +
		"subagent: exploring the codebase, answering how something works, " +
		"finding where a behavior lives. Give it a complete, specific brief; " +
		"it returns findings without modifying anything."
}

// Parameters implements agent.Tool.
func (t *Task) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt": prop("string", "The complete brief for the subagent, self-contained"),
	}, "prompt")
}

// Execute implements agent.Tool.
func (t *Task) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Prompt string `json:"prompt"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if t.Run == nil {
		return errResult("no subagent runner wired")
	}
	res, err := t.Run(ctx, a.Prompt)
	if err != nil {
		return errResult("subtask failed: %v", err)
	}
	// 子代理报告常含大段代码引用，必须截断后进入主对话（AGENTS.md：
	// 工具输出必须截断；issue #21：task 曾是唯一无上限的工具）。
	return agent.ToolResult{Content: truncateBytes([]byte(res.Text), taskHeadTail, taskHeadTail) +
		"\n\n（子代理 " + itoa2(res.Turns) + " 轮）"}
}

// taskHeadTail 是子代理报告的 head/tail 截断上限。
const taskHeadTail = 8 << 10

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Capability implements agent.Capabler: the subagent reads only.
func (t *Task) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
