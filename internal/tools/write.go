package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// Write creates or overwrites a file wholesale.
type Write struct{ ws Workspace }

// NewWrite builds the write tool.
func NewWrite(ws Workspace) *Write { return &Write{ws: ws} }

// Name implements agent.Tool.
func (w *Write) Name() string { return "write" }

// Description implements agent.Tool.
func (w *Write) Description() string {
	return "Create a file, or overwrite it with the full new content. " +
		"Parent directories are created. For small changes prefer edit."
}

// Parameters implements agent.Tool.
func (w *Write) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":    prop("string", "File path inside the workspace"),
		"content": prop("string", "The complete file content to write"),
	}, "path", "content")
}

// Execute implements agent.Tool.
func (w *Write) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	abs, err := w.ws.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	if dir := filepath.Dir(abs); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errResult("creating directory: %v", err)
		}
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return errResult("writing %s: %v", a.Path, err)
	}
	lines := strings.Count(a.Content, "\n")
	if !strings.HasSuffix(a.Content, "\n") && a.Content != "" {
		lines++
	}
	return okResult("wrote %s (%d lines, %d bytes)", a.Path, lines, len(a.Content))
}
