package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Edit replaces an exact, unique string in a file — the safest small-change
// primitive: what the model matched is exactly what changes.
type Edit struct{ ws Workspace }

// NewEdit builds the edit tool.
func NewEdit(ws Workspace) *Edit { return &Edit{ws: ws} }

// Name implements agent.Tool.
func (e *Edit) Name() string { return "edit" }

// Description implements agent.Tool.
func (e *Edit) Description() string {
	return "Replace an exact string in a file. old_string must match the file " +
		"content exactly (including whitespace) and be unique unless replace_all " +
		"is true. Read the file first to copy the exact text."
}

// Parameters implements agent.Tool.
func (e *Edit) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":        prop("string", "File path inside the workspace"),
		"old_string":  prop("string", "Exact text to replace, unique in the file"),
		"new_string":  prop("string", "Replacement text"),
		"replace_all": prop("boolean", "Replace every occurrence (default false)"),
	}, "path", "old_string", "new_string")
}

// Execute implements agent.Tool.
func (e *Edit) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.OldString == "" {
		return errResult("old_string is empty; write the file instead")
	}
	if a.OldString == a.NewString {
		return errResult("old_string and new_string are identical")
	}
	abs, err := e.ws.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return errResult("reading %s: %v", a.Path, err)
	}
	content := string(data)
	count := strings.Count(content, a.OldString)
	switch {
	case count == 0:
		return errResult("old_string not found in %s; read the file and copy the text exactly", a.Path)
	case count > 1 && !a.ReplaceAll:
		return errResult("old_string appears %d times in %s; add surrounding lines to make it unique or set replace_all", count, a.Path)
	}

	updated := strings.Replace(content, a.OldString, a.NewString, 1)
	if a.ReplaceAll {
		updated = strings.ReplaceAll(content, a.OldString, a.NewString)
	}
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return errResult("writing %s: %v", a.Path, err)
	}
	return okResult("replaced %d occurrence(s) in %s\n%s",
		count, a.Path, snippet(updated, a.NewString))
}

// snippet shows the replacement with one line of context around it.
func snippet(content, needle string) string {
	idx := strings.Index(content, needle)
	if idx < 0 {
		return ""
	}
	start := strings.LastIndexByte(content[:idx], '\n')
	if start < 0 {
		start = 0
	} else {
		start++
	}
	endRel := idx + len(needle)
	if endRel > len(content) {
		endRel = len(content)
	}
	end := strings.IndexByte(content[endRel:], '\n')
	if end < 0 {
		end = len(content)
	} else {
		end = endRel + end + 1
	}
	return fmt.Sprintf("---\n%s---", content[start:end])
}

// Capability implements agent.Capabler: editing changes one file at a time.
func (e *Edit) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// PathKey implements agent.PathGroupable: edit 是读-改-写语义，同路径
// 的并行调用必须顺序执行（issue #78）。
func (e *Edit) PathKey(args string) (string, bool) {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return "", false
	}
	return a.Path, true
}
