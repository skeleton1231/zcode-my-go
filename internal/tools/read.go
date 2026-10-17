package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Read shows a file with 1-based line numbers, the format the edit tool's
// old_string refers to.
type Read struct{ ws Workspace }

// NewRead builds the read tool.
func NewRead(ws Workspace) *Read { return &Read{ws: ws} }

// Name implements agent.Tool.
func (r *Read) Name() string { return "read" }

// Description implements agent.Tool.
func (r *Read) Description() string {
	return "Read a text file from the workspace, with 1-based line numbers. " +
		"Defaults to the first 2000 lines; use offset and limit for large files."
}

// Parameters implements agent.Tool.
func (r *Read) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":   prop("string", "File path, relative to the workspace or absolute inside it"),
		"offset": prop("number", "1-based line to start from (default 1)"),
		"limit":  prop("number", "Maximum lines to return (default 2000)"),
	}, "path")
}

// Execute implements agent.Tool.
func (r *Read) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	abs, err := r.ws.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	if r.ws.IsRemote() {
		return r.readRemote(a, abs)
	}

	f, err := os.Open(abs)
	if err != nil {
		return errResult("cannot read %s: %v", a.Path, err)
	}
	defer f.Close()

	// 拒绝明显非文本的文件：前 8KB 出现 NUL 即视为二进制。
	probe := make([]byte, 8192)
	n, _ := f.Read(probe)
	if bytes.IndexByte(probe[:n], 0) >= 0 {
		return errResult("%s looks like a binary file", a.Path)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return errResult("seeking %s: %v", a.Path, err)
	}

	offset := a.Offset
	if offset < 1 {
		offset = 1
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultReadLines
	}

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	shown := 0
	hitLineCap := false
	hitByteCap := false
	for scanner.Scan() {
		line++
		if line < offset {
			continue
		}
		// Past a cap, keep counting to report what remains, but stop
		// accumulating output.
		if shown >= limit {
			hitLineCap = true
			continue
		}
		if b.Len() >= readCapBytes {
			hitByteCap = true
			continue
		}
		fmt.Fprintf(&b, "%6d\t%s\n", line, scanner.Text())
		shown++
	}
	if err := scanner.Err(); err != nil {
		return errResult("reading %s: %v", a.Path, err)
	}
	if line == 0 {
		return okResult("(empty file)")
	}
	out := b.String()
	switch {
	case hitByteCap:
		out += fmt.Sprintf("… output truncated at %d bytes; narrow with offset/limit …\n", readCapBytes)
	case hitLineCap && line > offset+shown-1:
		out += fmt.Sprintf("… %d more lines; continue with offset=%d …\n",
			line-(offset+shown-1), offset+shown)
	}
	return agent.ToolResult{Content: out}
}

// Capability implements agent.Capabler: reading is safe to auto-allow.
func (r *Read) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// readRemote serves the read tool over the SSH workspace.
func (r *Read) readRemote(a struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}, abs string) agent.ToolResult {
	data, err := r.ws.Remote.ReadFile(remoteRel(r.ws, abs))
	if err != nil {
		return errResult("cannot read %s: %v", a.Path, err)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return errResult("%s looks like a binary file", a.Path)
	}
	offset := a.Offset
	if offset < 1 {
		offset = 1
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultReadLines
	}
	var b strings.Builder
	shown := 0
	hitLineCap, hitByteCap := false, false
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		if n < offset {
			continue
		}
		if shown >= limit {
			hitLineCap = true
			continue
		}
		if b.Len() >= readCapBytes {
			hitByteCap = true
			continue
		}
		fmt.Fprintf(&b, "%6d\t%s\n", n, line)
		shown++
	}
	out := b.String()
	if out == "" && len(data) == 0 {
		return okResult("(empty file)")
	}
	switch {
	case hitByteCap:
		out += fmt.Sprintf("… output truncated at %d bytes; narrow with offset/limit …\n", readCapBytes)
	case hitLineCap:
		out += "… more lines; continue with a larger limit …\n"
	}
	return agent.ToolResult{Content: out}
}

// remoteRel strips the workspace root from a resolved absolute path for
// the remote API (which resolves names against its own root).
func remoteRel(ws Workspace, abs string) string {
	root := strings.ReplaceAll(ws.Root, "\\", "/")
	p := strings.ReplaceAll(abs, "\\", "/")
	rel := strings.TrimPrefix(p, root)
	return strings.TrimPrefix(rel, "/")
}
