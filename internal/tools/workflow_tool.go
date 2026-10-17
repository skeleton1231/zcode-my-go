package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Workflow runs a multi-step plan (ZCode's dynamic workflow): research
// steps explore read-only, edit steps carry the session's permission gate.
type Workflow struct {
	// Run executes the plan; the service wires it to agent.RunWorkflow.
	Run func(ctx context.Context, steps []agent.WorkflowStep) ([]agent.StepResult, error)
}

// NewWorkflow builds the workflow tool over a runner.
func NewWorkflow(run func(ctx context.Context, steps []agent.WorkflowStep) ([]agent.StepResult, error)) *Workflow {
	return &Workflow{Run: run}
}

// Name implements agent.Tool.
func (w *Workflow) Name() string { return "workflow" }

// Description implements agent.Tool.
func (w *Workflow) Description() string {
	return "Run a multi-step plan: research steps (read-only investigation) and " +
		"edit steps (changes, gated by the session's permissions). Without " +
		"after-declarations steps run in order, each seeing the plan and prior " +
		"results; give steps names and declare after to run independent steps " +
		"in parallel (up to 4), each seeing its upstream results. Use for " +
		"tasks that naturally decompose. Keep plans under eight steps."
}

// Parameters implements agent.Tool.
func (w *Workflow) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"steps": map[string]any{
			"type":        "array",
			"description": "The plan, in execution order",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": prop("string", "Unique step name; required on every step once after is used"),
					"description": prop("string", "What this step does, self-contained"),
					"kind": map[string]any{
						"type":        "string",
						"description": "research (read-only) or edit (changes)",
						"enum":        []string{"research", "edit"},
					},
					"after": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Names of steps that must complete before this one; declaring any after switches the whole plan to parallel DAG scheduling",
					},
				},
				"required": []string{"description", "kind"},
			},
		},
	}, "steps")
}

// Execute implements agent.Tool.
func (w *Workflow) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Steps []agent.WorkflowStep `json:"steps"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if w.Run == nil {
		return errResult("no workflow runner wired")
	}
	results, err := w.Run(ctx, a.Steps)
	var b strings.Builder
	if err != nil {
		b.WriteString("error: " + err.Error() + "\n")
	}
	b.WriteString("工作流结果：\n")
	for i, res := range results {
		mark := "✓"
		if res.Failed {
			mark = "✕"
		}
		fmtStepLine(&b, mark, i+1, res.Step, res.Text)
	}
	if err != nil {
		return agent.ToolResult{IsError: true, Content: b.String()}
	}
	return agent.ToolResult{Content: b.String()}
}

func fmtStepLine(b *strings.Builder, mark string, n int, step agent.WorkflowStep, text string) {
	name := ""
	if step.Name != "" {
		name = step.Name + " "
	}
	b.WriteString(mark + " " + itoa3(n) + ". " + name + "[" + step.Kind + "] " + step.Description + "\n")
	if text != "" {
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
}

func itoa3(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Capability implements agent.Capabler: orchestration itself; edit steps
// are gated through the inherited permission service.
func (w *Workflow) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
