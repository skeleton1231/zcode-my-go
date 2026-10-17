package agent

import (
	"context"
	"encoding/json"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// ToolResult is a tool's outcome. Content is plain text shown to the model
// and the UI; IsError marks failures the model should react to.
type ToolResult struct {
	Content string
	IsError bool
}

// Tool is one capability the model may call. Implementations must honor the
// context (abort), validate their arguments, and cap their output size.
type Tool interface {
	Name() string
	Description() string
	// Parameters returns the JSON Schema of the arguments object.
	Parameters() map[string]any
	Execute(ctx context.Context, args json.RawMessage) ToolResult
}

// declarations translates tools to provider tool declarations.
func declarations(tools []Tool) []ai.ToolDeclaration {
	decls := make([]ai.ToolDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = ai.ToolDeclaration{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Parameters(),
		}
	}
	return decls
}
