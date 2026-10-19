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

// MaxParallelWorkflowSteps bounds concurrently running DAG steps (aligned
// with the subagent pool: both protect the provider from fan-out storms).
const MaxParallelWorkflowSteps = 4

// DAG step scheduling states, shared by the scheduler and the prompt
// builder (marks on the plan each step sees).
const (
	stepPending = iota
	stepDone
	stepFailed
	stepRunning
)

// WorkflowStep is one step of a plan. Sequential plans leave Name/After
// empty. Declaring After on any step switches the run to DAG scheduling:
// steps without pending dependencies run in parallel (bounded by
// MaxParallelWorkflowSteps) and each step's brief carries its direct
// upstream results instead of every prior result.
type WorkflowStep struct {
	Description string   `json:"description"`
	Kind        string   `json:"kind"` // research | edit
	Name        string   `json:"name,omitempty"`
	After       []string `json:"after,omitempty"` // dependencies by name
}

// StepResult is one step's outcome.
type StepResult struct {
	Step   WorkflowStep
	Text   string
	Failed bool
	// Usage 是该步骤 nested loop 的用量总计（spec 规则 88：工作流步
	// 骤是规则 87 注明的最后一个记账盲区）。
	Usage ai.Usage
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
	// Prefill 是续跑注入：下标 → 已完成步骤的既往结果（spec 规则
	// 48）。命中的步骤跳过执行、结果直接进入上下文——依赖它的下游
	// 照常看到文本；定义不可变，下标即稳定键。
	Prefill map[int]StepResult
}

// RunWorkflow executes a plan (ZCode's dynamic workflow). Without After
// declarations steps run in order, each seeing the plan and the results so
// far. With any After declaration the plan is a DAG: independent steps run
// in parallel, each seeing its direct upstream results. In both modes the
// first failure stops the run and reports what completed; in DAG mode
// already-running steps finish and undispatched dependents are reported as
// skipped.
func RunWorkflow(ctx context.Context, cfg WorkflowConfig, steps []WorkflowStep) ([]StepResult, error) {
	dag, err := ValidateWorkflowSteps(steps)
	if err != nil {
		return nil, err
	}
	if !dag {
		return runSequential(ctx, cfg, steps)
	}
	deps, _, err := stepGraph(steps)
	if err != nil {
		return nil, err
	}
	return runDAG(ctx, cfg, steps, deps)
}

// ValidateWorkflowSteps 校验步骤表（空表/未知类型/缺描述），并报告是
// 否进入 DAG 模式——持久化（保存定义）在执行前用同一套校验（spec 规
// 则 48）。
func ValidateWorkflowSteps(steps []WorkflowStep) (dag bool, err error) {
	if len(steps) == 0 {
		return false, fmt.Errorf("workflow has no steps")
	}
	for i, step := range steps {
		if step.Kind != StepResearch && step.Kind != StepEdit {
			return false, fmt.Errorf("step %d has unknown kind %q (research or edit)", i+1, step.Kind)
		}
		if strings.TrimSpace(step.Description) == "" {
			return false, fmt.Errorf("step %d has no description", i+1)
		}
		if len(step.After) > 0 {
			dag = true
		}
	}
	return dag, nil
}

// stepGraph resolves After names to step indices and validates the graph:
// unique non-empty names on every step (DAG mode is name-addressed),
// references that exist, no cycles (a cycle would leave steps forever
// undispatched, so it is rejected up front with Kahn's algorithm).
func stepGraph(steps []WorkflowStep) (deps, dependents [][]int, err error) {
	byName := make(map[string]int, len(steps))
	for i, s := range steps {
		if s.Name == "" {
			return nil, nil, fmt.Errorf("step %d (%s) lacks a name: required on every step once any step declares after", i+1, s.Kind)
		}
		if _, dup := byName[s.Name]; dup {
			return nil, nil, fmt.Errorf("duplicate step name %q", s.Name)
		}
		byName[s.Name] = i
	}
	deps = make([][]int, len(steps))
	dependents = make([][]int, len(steps))
	for i, s := range steps {
		for _, ref := range s.After {
			j, ok := byName[ref]
			if !ok {
				return nil, nil, fmt.Errorf("step %q depends on unknown step %q", s.Name, ref)
			}
			deps[i] = append(deps[i], j)
			dependents[j] = append(dependents[j], i)
		}
	}
	// 环检测：Kahn 归约必须覆盖全部步骤。入度 = 未满足的依赖数，
	// 归约方向沿 dependents（依赖完成 → 解锁下游）。
	indeg := make([]int, len(steps))
	for i, ds := range deps {
		indeg[i] = len(ds)
	}
	queue := make([]int, 0, len(steps))
	for i, d := range indeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	seen := 0
	for len(queue) > 0 {
		i := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		seen++
		for _, j := range dependents[i] {
			indeg[j]--
			if indeg[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if seen != len(steps) {
		return nil, nil, fmt.Errorf("dependency cycle in workflow steps (every after must eventually reach a step with no dependencies)")
	}
	return deps, dependents, nil
}

// runSequential is the no-After plan: strict order, every step briefed on
// the whole history so far, first failure stops.
func runSequential(ctx context.Context, cfg WorkflowConfig, steps []WorkflowStep) ([]StepResult, error) {
	var results []StepResult
	for i, step := range steps {
		if done, ok := cfg.Prefill[i]; ok && !done.Failed {
			results = append(results, done) // 续跑：已完成步骤注入既往结果
			continue
		}
		res, err := runStep(ctx, cfg, steps, results, i)
		results = append(results, res)
		if err != nil {
			return results, fmt.Errorf("step %d (%s) failed: %w", i+1, step.Kind, err)
		}
	}
	return results, nil
}

// runDAG schedules steps by readiness: a step dispatches once all its
// dependencies completed, up to MaxParallelWorkflowSteps concurrent runs.
// After a failure no new step dispatches; in-flight steps finish (their
// results are kept — aborting an edit step mid-run would leave half-done
// changes, so completion beats cancellation here) and dependents that never
// started are reported as skipped in the error.
func runDAG(ctx context.Context, cfg WorkflowConfig, steps []WorkflowStep, deps [][]int) ([]StepResult, error) {
	results := make([]StepResult, len(steps))
	state := make([]int, len(steps))
	inflight := 0
	failedAt := -1
	var firstErr error
	// 续跑预置：已完成步骤直接置 done、注入既往结果——就绪轮询与
	// 依赖上下文都从 state/results 取，无需特判（spec 规则 48）。
	for i, done := range cfg.Prefill {
		if i < len(steps) && !done.Failed {
			state[i] = stepDone
			results[i] = done
		}
	}

	type completion struct {
		i   int
		res StepResult
		err error
	}
	finished := make(chan completion)
	sem := make(chan struct{}, MaxParallelWorkflowSteps)

	// ready 轮询依赖状态而非维护入度表：失败后调度停摆，入度递减路径
	// 反而要多一份簿记（ZCode dynamic-workflow 同为就绪集语义）。
	ready := func(i int) bool {
		if state[i] != stepPending {
			return false
		}
		for _, j := range deps[i] {
			if state[j] != stepDone {
				return false
			}
		}
		return true
	}
	dispatch := func(i int) {
		state[i] = stepRunning
		inflight++
		sem <- struct{}{}
		prompt := dagPrompt(steps, results, state, deps[i], i)
		sub := subConfigFor(cfg, steps[i])
		go func() {
			// in-flight 步骤脱离取消跑完当前步骤（issue #96：注释承诺
			// completion beats cancellation——abort 当场杀掉执行中的编辑
			// 恰是注释自警的半成品风险）。失控由步骤自身的 MaxTurns 与
			// 工具超时兜底；新派发仍被 abort/failedAt 挡住。
			res, err := RunSubtask(context.WithoutCancel(ctx), sub, prompt)
			finished <- completion{i, StepResult{Step: steps[i], Text: res.Text, Failed: err != nil, Usage: res.Usage}, err}
		}()
	}

	for {
		if failedAt < 0 {
			for i := range steps {
				if inflight < MaxParallelWorkflowSteps && ready(i) {
					dispatch(i)
				}
			}
		}
		if inflight == 0 {
			break
		}
		c := <-finished
		<-sem
		inflight--
		results[c.i] = c.res
		if c.err != nil {
			state[c.i] = stepFailed
			if failedAt < 0 {
				failedAt, firstErr = c.i, c.err
			}
		} else {
			state[c.i] = stepDone
		}
	}

	if failedAt < 0 {
		out := make([]StepResult, 0, len(steps))
		for _, r := range results {
			out = append(out, r)
		}
		return out, nil
	}
	var skipped []string
	for i, s := range state {
		if s == stepPending {
			skipped = append(skipped, steps[i].Name)
		}
	}
	err := fmt.Errorf("step %q (%s) failed: %w", steps[failedAt].Name, steps[failedAt].Kind, firstErr)
	var out []StepResult
	for i, r := range results {
		if state[i] == stepDone || state[i] == stepFailed {
			out = append(out, r)
		}
	}
	if len(skipped) > 0 {
		err = fmt.Errorf("%w; skipped %d step(s): %s", err, len(skipped), strings.Join(skipped, ", "))
	}
	return out, err
}

// runStep runs one sequential step: the classic brief (whole plan, every
// prior result) and the step's subagent config.
func runStep(ctx context.Context, cfg WorkflowConfig, steps []WorkflowStep, prior []StepResult, i int) (StepResult, error) {
	prompt := workflowPrompt(steps, prior, i)
	sub := subConfigFor(cfg, steps[i])
	res, err := RunSubtask(ctx, sub, prompt)
	return StepResult{Step: steps[i], Text: res.Text, Failed: err != nil, Usage: res.Usage}, err
}

// subConfigFor maps a step kind to its nested-loop config: research runs
// read-only with the subagent system prompt; edit runs the full toolset
// with the session's system prompt and permission gate.
func subConfigFor(cfg WorkflowConfig, step WorkflowStep) SubtaskConfig {
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
	return sub
}

// dagPrompt builds a DAG step's brief: the plan with live statuses and
// only its direct upstream results (a step's inputs are what it declared).
func dagPrompt(steps []WorkflowStep, results []StepResult, state []int, deps []int, i int) string {
	var b strings.Builder
	b.WriteString("计划（多步工作流，按依赖并行调度）：\n")
	for j, s := range steps {
		mark := " "
		switch state[j] {
		case stepDone:
			mark = "✓"
		case stepFailed:
			mark = "✕"
		case stepRunning:
			if j == i {
				mark = "▶"
			} else {
				mark = "…"
			}
		}
		fmt.Fprintf(&b, "%s %d. %s[%s] %s\n", mark, j+1, namePrefix(s), s.Kind, s.Description)
	}
	if len(deps) > 0 {
		b.WriteString("\n上游步骤的结果：\n")
		for _, j := range deps {
			fmt.Fprintf(&b, "%s: %s\n", steps[j].Name, truncate(results[j].Text, 800))
		}
	}
	fmt.Fprintf(&b, "\n现在执行第 %d 步（%s）：%s\n只做这一步，完成后报告结果。",
		i+1, steps[i].Name, steps[i].Description)
	return b.String()
}

func namePrefix(s WorkflowStep) string {
	if s.Name == "" {
		return ""
	}
	return s.Name + " "
}

// workflowPrompt builds one sequential step's brief: the whole plan, prior
// results, and this step's assignment.
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
		fmt.Fprintf(&b, "%s %d. %s[%s] %s\n", mark, j+1, namePrefix(s), s.Kind, s.Description)
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

// truncate 按 rune 截断（issue #99：按字节切会把中文切成非法 UTF-8
// 直进子代理 prompt——#12/#40 同类第三例）。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
