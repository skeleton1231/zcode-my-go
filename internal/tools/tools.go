package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// Output caps, per AGENTS.md every tool defines one.
const (
	readCapBytes     = 60 << 10 // per read
	grepCapResults   = 200      // matching lines
	grepCapBytes     = 64 << 10
	bashCapBytes     = 30 << 10
	bashHeadTail     = bashCapBytes / 2
	globCapResults   = 500
	lsCapEntries     = 500
	defaultReadLines = 2000
)

// ReadOnly returns the read-only toolset for subagents.
func ReadOnly(workspace string) []agent.Tool {
	w := Workspace{Root: workspace}
	return []agent.Tool{NewRead(w), NewGrep(w), NewGlob(w), NewLs(w)}
}

// States holds the session-scoped tool state (the todo plan), keyed by
// session; All receives the owning session's slice.
type States struct {
	Todo *TodoState
	Jobs *Jobs
}

// NewStates builds empty per-session state.
func NewStates() *States { return &States{Todo: &TodoState{}, Jobs: NewJobs()} }

// All returns the built-in tools bound to a workspace. state may be nil —
// the todo tool then starts empty every turn — and ask may be nil, which
// makes the ask tool report no user channel.
func All(workspace string, state *States, ask func(ctx context.Context, q agent.Question) (string, error)) []agent.Tool {
	return AllW(Workspace{Root: workspace}, state, ask)
}

// AllW is All over a prebuilt workspace (an SSH workspace whose Remote
// is already dialed).
func AllW(w Workspace, state *States, ask func(ctx context.Context, q agent.Question) (string, error)) []agent.Tool {
	if state == nil {
		state = NewStates()
	}
	return []agent.Tool{
		NewRead(w),
		NewWrite(w),
		NewEdit(w),
		NewBash(w, state.Jobs),
		NewBashOutput(state.Jobs),
		NewBashKill(state.Jobs),
		NewGrep(w),
		NewGlob(w),
		NewLs(w),
		NewPatch(w),
		NewTodo(state.Todo),
		NewDispatch(nil), // service 重接（带 provider）
		NewAsk(ask),
	}
}

// decodeArgs unmarshals tool arguments; a decoding failure is a tool error
// the model can correct, not a panic.
func decodeArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

// errResult and okResult keep tool bodies terse.
func errResult(format string, args ...any) agent.ToolResult {
	return agent.ToolResult{IsError: true, Content: "error: " + fmt.Sprintf(format, args...)}
}

func okResult(format string, args ...any) agent.ToolResult {
	return agent.ToolResult{Content: fmt.Sprintf(format, args...)}
}

// truncateBytes keeps the head and the tail of oversized output with a
// marker in between, since both matter when diagnosing commands.
func truncateBytes(b []byte, head, tail int) string {
	if len(b) <= head+tail {
		return string(b)
	}
	return string(b[:head]) +
		fmt.Sprintf("\n… %d bytes truncated …\n", len(b)-head-tail) +
		string(b[len(b)-tail:])
}

// objectSchema is shorthand for a JSON object schema with properties.
func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// prop builds a {"type": t, "description": d} property.
func prop(t, d string) map[string]any {
	return map[string]any{"type": t, "description": d}
}
