package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Dispatch fans independent research questions out to parallel read-only
// subagents (ZCode's parallel subagents) and collects every answer in
// order — for questions that depend on each other, use task instead.
type Dispatch struct {
	Run func(ctx context.Context, prompts []string) []agent.SubtaskResult
}

// NewDispatch builds the tool over a parallel runner.
func NewDispatch(run func(ctx context.Context, prompts []string) []agent.SubtaskResult) *Dispatch {
	return &Dispatch{Run: run}
}

// Name implements agent.Tool.
func (d *Dispatch) Name() string { return "dispatch" }

// Description implements agent.Tool.
func (d *Dispatch) Description() string {
	return "Fan out 2-4 independent research questions to parallel read-only " +
		"subagents and collect all answers. Each prompt must be self-contained " +
		"and independent of the others' results; dependent or sequential work " +
		"belongs to task or workflow."
}

// Parameters implements agent.Tool.
func (d *Dispatch) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompts": map[string]any{
			"type":        "array",
			"description": "2-4 independent, self-contained research prompts",
			"items":       map[string]any{"type": "string"},
			"minItems":    2,
			"maxItems":    4,
		},
	}, "prompts")
}

// Execute implements agent.Tool.
func (d *Dispatch) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Prompts []string `json:"prompts"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if len(a.Prompts) < 2 {
		return errResult("dispatch needs at least 2 prompts (use task for one)")
	}
	if len(a.Prompts) > 4 {
		return errResult("dispatch caps at 4 prompts; split the work")
	}
	for i, p := range a.Prompts {
		if strings.TrimSpace(p) == "" {
			return errResult("prompt %d is empty", i+1)
		}
	}
	if d.Run == nil {
		return errResult("no dispatch runner wired")
	}
	results := d.Run(ctx, a.Prompts)
	var b strings.Builder
	for i, res := range results {
		// 每份报告按 task 工具同型截断（issue #92：4 份无限长回答
		// 直接拼接一次性进主对话上下文，比 #21 放大 4 倍）。
		fmt.Fprintf(&b, "## 任务 %d\n%s\n\n", i+1,
			truncateBytes([]byte(res.Text), taskHeadTail, taskHeadTail))
	}
	return agent.ToolResult{Content: strings.TrimRight(b.String(), "\n")}
}

// Capability implements agent.Capabler: the fan-out is read-only research.
func (d *Dispatch) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// SerialOnly implements agent.SerialOnly: a fan-out never overlaps inside
// a parallel batch (its own subagents already run concurrently).
func (d *Dispatch) SerialOnly() bool { return true }
