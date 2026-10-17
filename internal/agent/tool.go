package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
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

// SerialOnly marks tools that must never overlap with others in a
// parallel batch (pi's allowed-tools rule): interactive tools and tools
// whose state is one shared conversation.
type SerialOnly interface {
	SerialOnly() bool
}

// Capabler lets a tool declare its permission posture (ZCode's
// PermissionToolCapability): read-only tools skip approval, alwaysAsk tools
// confirm every call. Undeclared tools get the default (ask when the
// permission system is on).
type Capabler interface {
	Capability() permission.Capability
}

// ArgsCapabler lets a tool vary its capability per call — the bash tool's
// readonly policy (ZCode's bash-readonly-policy) decides per command.
type ArgsCapabler interface {
	CapabilityForArgs(args string) permission.Capability
}

// capabilityOf reads a tool's declaration, defaulting to medium risk.
func capabilityOf(t Tool) permission.Capability {
	if c, ok := t.(Capabler); ok {
		return c.Capability()
	}
	return permission.Capability{}
}

// capabilityFor reads the declaration for one call, per-args when the tool
// supports it.
func capabilityFor(t Tool, args string) permission.Capability {
	if ac, ok := t.(ArgsCapabler); ok {
		return ac.CapabilityForArgs(args)
	}
	return capabilityOf(t)
}

// AskRequest describes an approval the loop needs before executing
// (ZCode's permission broker asking the surface).
type AskRequest struct {
	SessionID string
	Tool      string
	Args      string
	Reason    string
	Risk      permission.RiskLevel
}

// AskResponse is the user's answer: Allow runs this call, Always also
// grants the session rule, anything else is a denial with a note for the
// model.
type AskResponse struct {
	Always bool
	Deny   bool
	Note   string
}

// Question is what the ask tool delivers to the user (ZCode's
// ask-user-question).
type Question struct {
	Text    string
	Options []string
}

// SummarizeArgs renders the canonical one-line argument summary permission
// rules match against (and the UI shows): object arguments as "k: v" pairs,
// anything else as trimmed text, capped.
func SummarizeArgs(args json.RawMessage) string {
	text := strings.TrimSpace(string(args))
	if text == "" || text == "{}" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(args, &parsed); err == nil && parsed != nil {
		var b strings.Builder
		for _, k := range sortedKeys(parsed) {
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString(k + ": " + previewValue(parsed[k]))
		}
		text = b.String()
	}
	if r := []rune(text); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return text
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func previewValue(v any) string {
	switch val := v.(type) {
	case string:
		r := []rune(val)
		if len(r) > 24 {
			return string(r[:23]) + "…"
		}
		return val
	case nil:
		return "null"
	default:
		s, _ := json.Marshal(v)
		return string(s)
	}
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
