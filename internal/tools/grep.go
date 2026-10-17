package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// skipDirs are never searched: version control state, dependencies, builds.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true,
	"dist": true, "build": true, ".mygo": true, ".venv": true, "__pycache__": true,
}

// Grep finds a regex across workspace files, ripgrep-style output.
type Grep struct{ ws Workspace }

// NewGrep builds the grep tool.
func NewGrep(ws Workspace) *Grep { return &Grep{ws: ws} }

// Name implements agent.Tool.
func (g *Grep) Name() string { return "grep" }

// Description implements agent.Tool.
func (g *Grep) Description() string {
	return "Search file contents with a regular expression across the workspace " +
		"(or under path). Output is path:line:text, capped at 200 matches. " +
		"Binary files and dependency directories are skipped."
}

// Parameters implements agent.Tool.
func (g *Grep) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"pattern": prop("string", "Regular expression (RE2 syntax)"),
		"path":    prop("string", "Subdirectory or file to search (default: the workspace root)"),
		"include": prop("string", "Glob the file name must match, e.g. *.go"),
	}, "pattern")
}

// Execute implements agent.Tool.
func (g *Grep) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return errResult("invalid pattern: %v", err)
	}
	rel := a.Path
	if rel == "" {
		rel = "."
	}
	root, err := g.ws.Resolve(rel)
	if err != nil {
		return errResult("%v", err)
	}
	var include *regexp.Regexp
	if a.Include != "" {
		// Glob 转正则：模型常给 *.go 这类过滤，一次转换即可。
		if include, err = globToRegex(a.Include); err != nil {
			return errResult("invalid include: %v", err)
		}
	}

	// 远程工作区：文件枚举经 Remote.Glob、内容经 Remote.ReadFile，
	// 匹配在本地执行，输出与本地路径同格式、同上限（review #83：曾落
	// 回本地 WalkDir，远程会话上永远零匹配）。
	if g.ws.IsRemote() {
		return g.grepRemote(re, include)
	}

	var b strings.Builder
	matches := 0
	files := 0
	truncated := false
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if include != nil && !include.MatchString(name) {
			return nil
		}
		n, stop := grepFile(path, g.ws.Root, re, &b, &matches)
		files += n
		if stop {
			truncated = true
			return errStopWalking
		}
		return nil
	})
	if walkErr != nil && walkErr != errStopWalking && walkErr != ctx.Err() {
		return errResult("%v", walkErr)
	}
	out := b.String()
	if out == "" {
		if ctx.Err() != nil {
			return errResult("canceled")
		}
		return okResult("no matches for %q", a.Pattern)
	}
	if truncated || b.Len() >= grepCapBytes {
		out = truncateBytes([]byte(out), grepCapBytes, 0)
		out += "… results truncated …\n"
	}
	return agent.ToolResult{Content: fmt.Sprintf("%d matches in %d files\n%s", matches, files, out)}
}

// errStopWalking stops the walk once caps are hit.
var errStopWalking = fmt.Errorf("caps reached")

// grepFile appends matches of one file; reports whether caps were hit.
func grepFile(path, root string, re *regexp.Regexp, b *strings.Builder, matches *int) (files int, stop bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	probe := make([]byte, 8192)
	n, _ := f.Read(probe)
	if bytes.IndexByte(probe[:n], 0) >= 0 {
		return 0, false // binary
	}
	if _, err := f.Seek(0, 0); err != nil {
		return 0, false
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	found := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if re.MatchString(scanner.Text()) {
			fmt.Fprintf(b, "%s:%d:%s\n", filepath.ToSlash(rel), line, scanner.Text())
			*matches++
			found = true
			if *matches >= grepCapResults || b.Len() >= grepCapBytes {
				return 1, true
			}
		}
	}
	if found {
		return 1, false
	}
	return 0, false
}

// globToRegex converts a filename glob (*.go) to an anchored regex.
func globToRegex(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range glob {
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
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Capability implements agent.Capabler: searching is safe to auto-allow.
func (g *Grep) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// grepRemote 在远程工作区上搜索：Glob 枚举 + ReadFile 逐行匹配，
// 二进制跳过与上限语义与本地 grepFile 一致。
func (g *Grep) grepRemote(re *regexp.Regexp, include *regexp.Regexp) agent.ToolResult {
	// ** 只列目录，**/* 才含文件（remote.Glob 的语义），目录经 Stat 剔除。
	names, err := g.ws.Remote.Glob("**/*")
	if err != nil {
		return errResult("enumerating remote files: %v", err)
	}
	var b strings.Builder
	matches, files := 0, 0
	for _, name := range names {
		if matches >= grepCapResults || b.Len() >= grepCapBytes {
			break
		}
		if _, isDir, serr := g.ws.Remote.Stat(name); serr == nil && isDir {
			continue
		}
		base := path.Base(name)
		if include != nil && !include.MatchString(base) {
			continue
		}
		data, err := g.ws.Remote.ReadFile(name)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			continue // 读失败与二进制跳过，与本地语义一致
		}
		found := false
		line := 0
		for _, text := range strings.Split(string(data), "\n") {
			line++
			if re.MatchString(text) {
				fmt.Fprintf(&b, "%s:%d:%s\n", name, line, text)
				matches++
				found = true
				if matches >= grepCapResults || b.Len() >= grepCapBytes {
					break
				}
			}
		}
		if found {
			files++
		}
	}
	out := b.String()
	if out == "" {
		return okResult("no matches")
	}
	return agent.ToolResult{Content: fmt.Sprintf("匹配 %d 处（%d 个文件）\n%s",
		matches, files, truncateBytes([]byte(out), grepCapBytes, 0))}
}
