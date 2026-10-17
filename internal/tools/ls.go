package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// Ls lists a directory's entries.
type Ls struct{ ws Workspace }

// NewLs builds the ls tool.
func NewLs(ws Workspace) *Ls { return &Ls{ws: ws} }

// Name implements agent.Tool.
func (l *Ls) Name() string { return "ls" }

// Description implements agent.Tool.
func (l *Ls) Description() string {
	return "List a directory's entries: directories first, then files, each " +
		"with size and modification time. Defaults to the workspace root, " +
		"capped at 500 entries."
}

// Parameters implements agent.Tool.
func (l *Ls) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path": prop("string", "Directory to list (default: the workspace root)"),
	})
}

// Execute implements agent.Tool.
func (l *Ls) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	rel := a.Path
	if rel == "" {
		rel = "."
	}
	abs, err := l.ws.Resolve(rel)
	if err != nil {
		return errResult("%v", err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return errResult("reading %s: %v", rel, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	var b strings.Builder
	shown := 0
	for _, e := range entries {
		if shown >= lsCapEntries {
			fmt.Fprintf(&b, "… truncated at %d entries …\n", lsCapEntries)
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "d %10s  %s/\n", humanSize(info.Size()), e.Name())
		} else {
			fmt.Fprintf(&b, "- %10s  %s\n", humanSize(info.Size()), e.Name())
		}
		shown++
	}
	if shown == 0 {
		return okResult("(empty directory)")
	}
	return agent.ToolResult{Content: b.String()}
}

// humanSize renders a byte count readably.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d", n)
	}
}
