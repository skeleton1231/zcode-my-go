package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// WorkflowRequest 是一次工作流调用的三种形态（spec 规则 48）：临场步
// 骤表（可附带 saveAs 命名保存）、按名执行已保存定义、按运行 ID 续跑
// 未完记录。三者互斥，steps 优先。
type WorkflowRequest struct {
	Steps  []agent.WorkflowStep
	SaveAs string
	Run    string
	Resume string
}

// WorkflowOutcome 是执行的回执：结果、运行 ID（记录可续跑）与保存名。
type WorkflowOutcome struct {
	Results []agent.StepResult
	RunID   string
	SavedAs string
}

// Workflow runs a multi-step plan (ZCode's dynamic workflow): research
// steps explore read-only, edit steps carry the session's permission gate.
type Workflow struct {
	// Exec executes the request; the service wires it to agent.RunWorkflow
	// with persistence (definition store + run records).
	Exec func(ctx context.Context, req WorkflowRequest) (WorkflowOutcome, error)
}

// NewWorkflow builds the workflow tool over an executor.
func NewWorkflow(exec func(ctx context.Context, req WorkflowRequest) (WorkflowOutcome, error)) *Workflow {
	return &Workflow{Exec: exec}
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
		"tasks that naturally decompose. Keep plans under eight steps. " +
		"Reusable plans: saveAs names the plan for later {run: name} calls; " +
		"{resume: runID} continues an interrupted run, skipping steps that " +
		"already completed."
}

// Parameters implements agent.Tool.
func (w *Workflow) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"run":    prop("string", "Name of a saved workflow to execute (mutually exclusive with steps)"),
		"resume": prop("string", "ID of an interrupted run to continue: completed steps are skipped, their recorded results feed downstream steps"),
		"saveAs": prop("string", "Name to save these steps as for later reuse (lowercase letters, digits, hyphens)"),
		"steps": map[string]any{
			"type":        "array",
			"description": "The plan, in execution order",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        prop("string", "Unique step name; required on every step once after is used"),
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
		Steps  []agent.WorkflowStep `json:"steps"`
		SaveAs string               `json:"saveAs"`
		Run    string               `json:"run"`
		Resume string               `json:"resume"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if w.Exec == nil {
		return errResult("no workflow runner wired")
	}
	req := WorkflowRequest{Steps: a.Steps, SaveAs: strings.TrimSpace(a.SaveAs),
		Run: strings.TrimSpace(a.Run), Resume: strings.TrimSpace(a.Resume)}
	if len(req.Steps) == 0 && req.Run == "" && req.Resume == "" {
		return errResult("workflow needs steps, run (saved name) or resume (run ID)")
	}
	outcome, err := w.Exec(ctx, req)
	var b strings.Builder
	if err != nil {
		b.WriteString("error: " + err.Error() + "\n")
	}
	if outcome.SavedAs != "" {
		b.WriteString("已保存工作流 " + outcome.SavedAs + "，之后可用 run 参数按名执行\n")
	}
	if outcome.RunID != "" {
		b.WriteString("运行记录 " + outcome.RunID + "\n")
	}
	b.WriteString("工作流结果：\n")
	for i, res := range outcome.Results {
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
