package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// Step kinds: research steps run read-only; edit steps run the full toolset
// and go through the permission gate (ZCode's workflow step kinds).
const (
	StepResearch = "research"
	StepEdit     = "edit"
)

// WorkflowStep is one step of a plan.
type WorkflowStep struct {
	Description string `json:"description"`
	Kind        string `json:"kind"` // research | edit
}

// StepResult is one step's outcome.
type StepResult struct {
	Step   WorkflowStep
	Text   string
	Failed bool
}

// WorkflowConfig extends SubtaskConfig with the editable side: edit steps
// run the full tools and pass the session's permission gate, so "always
// allow" grants and plan mode carry into the workflow.
type WorkflowConfig struct {
	Provider    ai.Provider
	Model       string
	System      string
	ReadOnly    []Tool // research steps
	Editable    []Tool // edit steps
	Permissions *permission.Service
	Ask         func(AskRequest) AskResponse
}

// RunWorkflow executes steps in order (ZCode's dynamic workflow): each
// step's prompt carries the plan and the results so far; the first failure
// stops the run and reports what completed.
func RunWorkflow(ctx context.Context, cfg WorkflowConfig, steps []WorkflowStep) ([]StepResult, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("workflow has no steps")
	}
	for i, step := range steps {
		if step.Kind != StepResearch && step.Kind != StepEdit {
			return nil, fmt.Errorf("step %d has unknown kind %q (research or edit)", i+1, step.Kind)
		}
		if strings.TrimSpace(step.Description) == "" {
			return nil, fmt.Errorf("step %d has no description", i+1)
		}
	}

	var results []StepResult
	for i, step := range steps {
		prompt := workflowPrompt(steps, results, i)
		sub := SubtaskConfig{
			Provider:  cfg.Provider,
			Model:     cfg.Model,
			Tools:     cfg.ReadOnly,
			MaxTurns:  25,
			MaxTokens: 16384,
		}
		if step.Kind == StepEdit {
			sub.Tools = cfg.Editable
			sub.System = cfg.System
			sub.Permissions = cfg.Permissions // edit 步骤同受权限门与会话规则
			sub.Ask = cfg.Ask
		} else {
			sub.System = SubtaskSystem()
		}
		res, err := RunSubtask(ctx, sub, prompt)
		results = append(results, StepResult{Step: step, Text: res.Text, Failed: err != nil})
		if err != nil {
			return results, fmt.Errorf("step %d (%s) failed: %w", i+1, step.Kind, err)
		}
	}
	return results, nil
}

// workflowPrompt builds one step's brief: the whole plan, prior results,
// and this step's assignment.
func workflowPrompt(steps []WorkflowStep, done []StepResult, i int) string {
	var b strings.Builder
	b.WriteString("计划（多步工作流）：\n")
	for j, s := range steps {
		mark := " "
		switch {
		case j < len(done) && !done[j].Failed:
			mark = "✓"
		case j < len(done):
			mark = "✕"
		case j == i:
			mark = "▶"
		}
		fmt.Fprintf(&b, "%s %d. [%s] %s\n", mark, j+1, s.Kind, s.Description)
	}
	if len(done) > 0 {
		b.WriteString("\n此前步骤的结果：\n")
		for j, r := range done {
			fmt.Fprintf(&b, "%d. %s\n", j+1, truncate(r.Text, 800))
		}
	}
	fmt.Fprintf(&b, "\n现在执行第 %d 步：%s\n只做这一步，完成后报告结果。", i+1, steps[i].Description)
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
