package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/textdiff"
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
	if w.ws.IsRemote() {
		rel := remoteRel(w.ws, abs)
		oldData, _ := w.ws.Remote.ReadFile(rel) // 读不到即新文件：diff 自空
		if err := w.ws.Remote.WriteFile(rel, []byte(a.Content), 0o644); err != nil {
			return errResult("writing %s: %v", a.Path, err)
		}
		lines := strings.Count(a.Content, "\n")
		if !strings.HasSuffix(a.Content, "\n") && a.Content != "" {
			lines++
		}
		return agent.ToolResult{Content: fmt.Sprintf("wrote %s (%d lines, %d bytes) [remote]",
			a.Path, lines, len(a.Content)), Diff: textdiff.Unified(oldData, []byte(a.Content))}
	}
	if dir := filepath.Dir(abs); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errResult("creating directory: %v", err)
		}
	}
	oldData, _ := os.ReadFile(abs) // 覆盖前读旧内容；读不到即新文件，diff 自空
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return errResult("writing %s: %v", a.Path, err)
	}
	lines := strings.Count(a.Content, "\n")
	if !strings.HasSuffix(a.Content, "\n") && a.Content != "" {
		lines++
	}
	return agent.ToolResult{Content: fmt.Sprintf("wrote %s (%d lines, %d bytes)",
		a.Path, lines, len(a.Content)), Diff: textdiff.Unified(oldData, []byte(a.Content))}
}

// Capability implements agent.Capabler: writing files changes the workspace.
func (w *Write) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// PathKey implements agent.PathGroupable: write 是读-改-写语义，同路径
// 的并行调用必须顺序执行（issue #78）。
func (w *Write) PathKey(args string) (string, bool) {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return "", false
	}
	return a.Path, true
}
