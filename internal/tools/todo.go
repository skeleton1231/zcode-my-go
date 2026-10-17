package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// TodoStatus of a task.
type TodoStatus string

// Task statuses.
const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoDone       TodoStatus = "done"
)

// TodoTask is one entry of the plan (ZCode's task list tool).
type TodoTask struct {
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// TodoState holds the plan across turns; one per session.
type TodoState struct {
	mu    sync.Mutex
	tasks []TodoTask
}

// Todo lets the model track its plan (ZCode/Claude-Code-style): read the
// list, or replace it wholesale as work progresses.
type Todo struct{ state *TodoState }

// NewTodo builds the todo tool over session-scoped state.
func NewTodo(state *TodoState) *Todo { return &Todo{state: state} }

// Name implements agent.Tool.
func (t *Todo) Name() string { return "todo" }

// Description implements agent.Tool.
func (t *Todo) Description() string {
	return "Track the plan for the current task. op=read lists the tasks; " +
		"op=write replaces the whole list. Use one in_progress task at a time; " +
		"mark tasks done as you finish them and add new ones as they emerge."
}

// Parameters implements agent.Tool.
func (t *Todo) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"op": map[string]any{
			"type":        "string",
			"description": "read the list, or write a new one",
			"enum":        []string{"read", "write"},
		},
		"tasks": map[string]any{
			"type":        "array",
			"description": "op=write: the full task list, in order",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": prop("string", "What to do"),
					"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}},
				},
				"required": []string{"content", "status"},
			},
		},
	}, "op")
}

// Execute implements agent.Tool.
func (t *Todo) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Op    string     `json:"op"`
		Tasks []TodoTask `json:"tasks"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	switch a.Op {
	case "read":
		return agent.ToolResult{Content: t.render()}
	case "write":
		if a.Tasks == nil {
			return errResult("op=write needs tasks")
		}
		t.state.mu.Lock()
		t.state.tasks = append([]TodoTask(nil), a.Tasks...)
		t.state.mu.Unlock()
		return agent.ToolResult{Content: t.render()}
	default:
		return errResult("unknown op %q (read or write)", a.Op)
	}
}

// render formats the list with progress marks.
func (t *Todo) render() string {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	if len(t.state.tasks) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for i, task := range t.state.tasks {
		mark := " "
		switch task.Status {
		case TodoInProgress:
			mark = "▶"
		case TodoDone:
			mark = "✓"
		}
		fmt.Fprintf(&b, "%s %d. %s\n", mark, i+1, task.Content)
	}
	done := 0
	for _, task := range t.state.tasks {
		if task.Status == TodoDone {
			done++
		}
	}
	fmt.Fprintf(&b, "\n%d/%d done", done, len(t.state.tasks))
	return b.String()
}

// Capability implements agent.Capabler: the plan is session state only.
func (t *Todo) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
