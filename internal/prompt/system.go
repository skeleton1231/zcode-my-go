// Package prompt builds the agent's system prompt, including the runtime
// environment (ZCode's environment.ts) and the workspace's project context
// file (ZGO.md / AGENTS.md / CLAUDE.md).
package prompt

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// contextFiles are looked up in the workspace root, first match wins.
var contextFiles = []string{"ZGO.md", "AGENTS.md", "CLAUDE.md"}

// contextCapBytes bounds the project context injected into the prompt.
const contextCapBytes = 8 << 10

// gitTimeout bounds each best-effort git query.
const gitTimeout = 3 * time.Second

// System builds the workspace-aware system prompt（稳定块 + 易变块的拼接，
// 非缓存路径用；缓存路径分开取 SystemStable/SystemVolatile——git dirty
// 计数每轮变化，混在缓存前缀里会整块失效，issue #139）。
func System(workspace string) string {
	return SystemStable(workspace) + SystemVolatile(workspace)
}

// SystemStable is the cacheable system prefix: identity, environment,
// project context, doctrine——跨轮不变（日期按天粒度）。
func SystemStable(workspace string) string {
	shell := "bash"
	if runtime.GOOS == "windows" {
		shell = "PowerShell"
	}
	var b strings.Builder
	b.WriteString(`You are Zgo, a coding agent working in the user's workspace.

Environment:
- Platform: ` + runtime.GOOS + "/" + runtime.GOARCH + " (Go " + runtime.Version() + ")\n" +
		"- Shell: " + shell + " (the bash tool uses it)\n" +
		"- Workspace: " + workspace + "\n" +
		"- Date: " + time.Now().Format("2006-01-02") + "\n")

	if ctx := projectContext(workspace); ctx != "" {
		b.WriteString("\nProject context (from the workspace's context file):\n" + ctx + "\n")
	}

	b.WriteString(`
How to work:
- Explore before you change: read the files you touch, and prefer ls/glob/grep
  to find them. Match the surrounding code's style, naming and comment density.
- Make focused, minimal changes; verify them by running the project's checks
  or tests when possible, and report results honestly, including failures.
- All file paths resolve inside the workspace; paths outside it are errors.
- Prefer the edit tool for small changes and write for new files or full
  rewrites. Read a file before editing it.
- Keep answers concise. When you finish a task, summarize what changed.`)
	return b.String()
}

// SystemVolatile is the uncacheable system tail: the git summary whose
// dirty count changes every edit——编辑会话主场景，绝不能进缓存前缀。
func SystemVolatile(workspace string) string {
	if git := gitSummary(workspace); git != "" {
		return "\nRepository:\n- Git: " + git + "\n"
	}
	return ""
}

// gitSummary reports the branch and a dirty/clean hint, best-effort: no
// git, no repo or a slow command simply drops the line.
func gitSummary(workspace string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	branch, err := runGit(ctx, workspace, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	status, err := runGit(ctx, workspace, "status", "--porcelain")
	if err != nil {
		return strings.TrimSpace(branch)
	}
	if status == "" {
		return strings.TrimSpace(branch) + " (clean)"
	}
	lines := strings.Count(status, "\n")
	if !strings.HasSuffix(status, "\n") && status != "" {
		lines++
	}
	return strings.TrimSpace(branch) + " (dirty: " + itoa(lines) + " changes)"
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// projectContext loads the first context file present in the workspace.
func projectContext(workspace string) string {
	for _, name := range contextFiles {
		data, err := readFileLimited(joinPath(workspace, name), contextCapBytes)
		if err != nil || len(data) == 0 {
			continue
		}
		return string(data)
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
