package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Tool adapts one MCP tool definition to the agent Tool interface.
type Tool struct {
	Server *Server
	Def    ToolDef
}

// Name implements agent.Tool: prefixed by the server to avoid collisions.
func (t *Tool) Name() string {
	name := "mcp_" + sanitize(t.Server.Name) + "_" + sanitize(t.Def.Name)
	return name
}

// Description implements agent.Tool.
func (t *Tool) Description() string {
	if t.Def.Description != "" {
		return t.Def.Description
	}
	return fmt.Sprintf("Tool %q of MCP server %q.", t.Def.Name, t.Server.Name)
}

// Parameters implements agent.Tool.
func (t *Tool) Parameters() map[string]any {
	if t.Def.InputSchema != nil {
		return t.Def.InputSchema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// Execute implements agent.Tool.
func (t *Tool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	out, err := t.Server.Call(ctx, t.Def.Name, args)
	if err != nil {
		return agent.ToolResult{IsError: true, Content: "error: " + err.Error()}
	}
	if out == "" {
		out = "(no output)"
	}
	return agent.ToolResult{Content: out}
}

// Capability implements agent.Capabler: MCP tools are external processes —
// medium risk, ask before running.
func (t *Tool) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// AgentTools adapts all of a server's tools.
func AgentTools(s *Server) []agent.Tool {
	defs := s.Tools()
	out := make([]agent.Tool, len(defs))
	for i, def := range defs {
		out[i] = &Tool{Server: s, Def: def}
	}
	return out
}

// sanitize makes a name safe for tool-name matching.
func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
}
