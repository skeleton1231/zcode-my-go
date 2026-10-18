package tools

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Glob expands a path pattern, including ** for any depth.
type Glob struct {
	ws Workspace
	ig *ignoreList // .gitignore 规则（issue #208），会话级加载
}

// NewGlob builds the glob tool.
func NewGlob(ws Workspace) *Glob { return &Glob{ws: ws, ig: loadIgnores(ws)} }

// Name implements agent.Tool.
func (g *Glob) Name() string { return "glob" }

// Description implements agent.Tool.
func (g *Glob) Description() string {
	return "List workspace files matching a glob pattern (** matches any " +
		"depth), e.g. **/*.go or src/*.ts. Results are workspace-relative, " +
		"capped at 500."
}

// Parameters implements agent.Tool.
func (g *Glob) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"pattern": prop("string", "Glob pattern, ** matches any directory depth"),
		"path":    prop("string", "Directory to search in (default: the workspace root)"),
	}, "pattern")
}

// Execute implements agent.Tool.
func (g *Glob) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.Pattern == "" {
		return errResult("pattern is empty")
	}
	re, err := pathGlobToRegex(a.Pattern)
	if err != nil {
		return errResult("invalid pattern: %v", err)
	}
	base := a.Path
	if base == "" {
		base = "."
	}
	root, err := g.ws.Resolve(base)
	if err != nil {
		return errResult("%v", err)
	}

	// 远程工作区：模式交给服务端 glob（支持 **），结果按根相对化
	// 输出（review #83：曾落回本地 WalkDir，远程会话上永远零匹配）。
	if g.ws.IsRemote() {
		pattern := filepath.ToSlash(a.Pattern)
		if rel := remoteRel(g.ws, root); rel != "" {
			pattern = rel + "/" + pattern
		}
		names, gerr := g.ws.Remote.Glob(pattern)
		if gerr != nil {
			return errResult("%v", gerr)
		}
		prefix := remoteRel(g.ws, root)
		var out []string
		for _, n := range names {
			rel := n
			if prefix != "" {
				rel = strings.TrimPrefix(strings.TrimPrefix(n, prefix), "/")
			}
			out = append(out, rel)
			if len(out) >= globCapResults {
				break
			}
		}
		if len(out) == 0 {
			return okResult("no files match %q", a.Pattern)
		}
		return agent.ToolResult{Content: strings.Join(out, "\n")}
	}

	var matches []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || g.ig.match(relToRoot(root, path), true)) {
				return filepath.SkipDir
			}
			return nil
		}
		if g.ig.match(relToRoot(root, path), false) {
			return nil // .gitignore 命中（issue #208）
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if re.MatchString(filepath.ToSlash(rel)) {
			matches = append(matches, filepath.ToSlash(rel))
			if len(matches) >= globCapResults {
				return errStopWalking
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != errStopWalking && walkErr != ctx.Err() {
		return errResult("%v", walkErr)
	}
	if ctx.Err() != nil {
		return errResult("canceled")
	}
	if len(matches) == 0 {
		return okResult("no files match %q", a.Pattern)
	}
	sort.Strings(matches)
	truncated := ""
	if len(matches) >= globCapResults {
		truncated = "\n… results truncated …"
	}
	return agent.ToolResult{Content: strings.Join(matches, "\n") + truncated}
}

// pathGlobToRegex converts a full-path glob to an anchored regex over
// slash-separated paths; ** spans separators.
func pathGlobToRegex(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	parts := strings.Split(filepath.ToSlash(glob), "/")
	for _, part := range parts {
		if part == "**" {
			b.WriteString("(?:[^/]*/)*")
			continue
		}
		for _, r := range part {
			switch r {
			case '*':
				b.WriteString("[^/]*")
			case '?':
				b.WriteString("[^/]")
			case '.', '(', ')', '+', '|', '^', '$', '[', ']', '{', '}', '\\':
				b.WriteString("\\" + string(r))
			default:
				b.WriteRune(r)
			}
		}
		b.WriteString("/")
	}
	b.WriteString("$")
	// Trailing "/$" was appended per part including the last; a file pattern
	// without a trailing slash must match too, so relax the final slash.
	expr := strings.TrimSuffix(b.String(), "/$")
	if !strings.HasSuffix(expr, "/") && !strings.HasSuffix(expr, "(?:[^/]*/)*") {
		expr += "(?:/|$)"
	}
	return regexp.Compile(expr)
}

// Capability implements agent.Capabler: listing paths is safe to auto-allow.
func (g *Glob) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
