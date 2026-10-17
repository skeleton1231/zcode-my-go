package tools

import (
	"context"
	"encoding/json"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Ask presents a question to the user and waits for the answer (ZCode's
// ask-user-question): the model asks when it needs a decision, a
// preference or information only the user has.
type Ask struct {
	// fn delivers the question; it blocks until answered.
	fn func(ctx context.Context, q agent.Question) (string, error)
}

// NewAsk builds the ask tool over a delivery callback.
func NewAsk(fn func(ctx context.Context, q agent.Question) (string, error)) *Ask {
	return &Ask{fn: fn}
}

// Name implements agent.Tool.
func (a *Ask) Name() string { return "ask" }

// Description implements agent.Tool.
func (a *Ask) Description() string {
	return "Ask the user a question when you need a decision, a preference, " +
		"or information you cannot find in the workspace. Provide concrete " +
		"options when the answer is a choice. Do not use this for things you " +
		"can determine yourself."
}

// Parameters implements agent.Tool.
func (a *Ask) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"question": prop("string", "The question, specific and self-contained"),
		"options": map[string]any{
			"type":        "array",
			"description": "Optional choices; keep under six, each a short label",
			"items":       map[string]any{"type": "string"},
		},
	}, "question")
}

// Execute implements agent.Tool.
func (a *Ask) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var q struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := decodeArgs(args, &q); err != nil {
		return errResult("%v", err)
	}
	if n := len(q.Options); n > 6 {
		return errResult("too many options (%d); keep under six", n)
	}
	if a.fn == nil {
		return errResult("no user channel in this session")
	}
	answer, err := a.fn(ctx, agent.Question{Text: q.Question, Options: q.Options})
	if err != nil {
		return errResult("asking the user: %v", err)
	}
	return agent.ToolResult{Content: "用户回答：" + answer}
}

// Capability implements agent.Capabler: asking has no side effects.
func (a *Ask) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
