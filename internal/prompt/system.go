// Package prompt builds the agent's system prompt, including the runtime
// environment (ZCode's environment.ts) and the workspace's project context
// files (ZGO.md / AGENTS.md / CLAUDE.md) collected as a hierarchy.
package prompt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// contextFiles are looked up per directory, first match wins.
var contextFiles = []string{"ZGO.md", "AGENTS.md", "CLAUDE.md"}

// contextCapBytes bounds the total context injected into the prompt.
const contextCapBytes = 8 << 10

// contextMaxDepth bounds the up-walk from workspace to git top.
const contextMaxDepth = 16

// gitTimeout bounds each best-effort git query.
const gitTimeout = 3 * time.Second

// System builds the workspace-aware system prompt（稳定块 + 易变块的拼接，
// 非缓存路径用；缓存路径分开取 SystemStable/SystemVolatile——git dirty
// 计数每轮变化，混在缓存前缀里会整块失效，issue #139）。
func System(workspace string) string {
	return SystemFS(workspace, nil)
}

// SystemFS 同 System：read 非 nil 时上下文文件经其根相对读取（远程工作
// 区的 RemoteFS.ReadFile——此前远程会话读本地路径必然落空、静默丢失项
// 目上下文），且只读根层（远程 git 顶不可知，不做向上回溯）。
func SystemFS(workspace string, read func(name string) ([]byte, error)) string {
	return SystemStableFS(workspace, read) + SystemVolatile(workspace)
}

// SystemStable is the cacheable system prefix: identity, environment,
// project context, doctrine——跨轮不变（日期按天粒度）。
func SystemStable(workspace string) string { return SystemStableFS(workspace, nil) }

func SystemStableFS(workspace string, read func(name string) ([]byte, error)) string {
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

	if ctx := projectContext(workspace, read); ctx != "" {
		b.WriteString("\nProject context (hierarchy: user → repo root → workspace):\n" + ctx + "\n")
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

// projectContext collects the context hierarchy（ZCode context-hierarchy
// 的落地）：全局用户文件（~/.zgo/ZGO.md）+ 自工作区向上到 git 顶（含）
// 每层第一个命中的上下文文件，浅→深拼接（一般→具体）；总量封顶
// contextCapBytes，超限截断并说明。read 非 nil（远程）只读根层。
func projectContext(workspace string, read func(name string) ([]byte, error)) string {
	var parts []string
	if g := globalContext(); g != "" {
		parts = append(parts, "--- user (~/.zgo/ZGO.md) ---\n"+g)
	}
	for _, dir := range dirChain(workspace, read) {
		for _, name := range contextFiles {
			data := readContextFile(dir, name, read)
			if len(data) == 0 {
				continue
			}
			parts = append(parts, "--- "+dirLabel(workspace, dir)+" ---\n"+string(data))
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	joined := strings.Join(parts, "\n\n")
	if len(joined) > contextCapBytes {
		joined = truncateRunes(joined, contextCapBytes) + "\n…(context capped at 8KB)"
	}
	return joined
}

// dirChain lists the up-walk chain, shallow → deep（一般 → 具体）：read
// 非 nil（远程）只含工作区根；本地自工作区向上到 git 顶（含）。git 顶
// 必须是工作区的真祖先——HOME 自身是 git 仓库的机器上 show-toplevel 会
// 给出毫不相关的顶，此时退回单层（issue #98 的教训：仓库判定必须
// show-toplevel 严格归属）。
func dirChain(workspace string, read func(name string) ([]byte, error)) []string {
	if read != nil {
		return []string{workspace}
	}
	top := gitToplevel(workspace)
	if top == "" || !isAncestor(top, workspace) {
		return []string{workspace}
	}
	var deep []string
	for dir := workspace; len(deep) < contextMaxDepth; dir = filepath.Dir(dir) {
		deep = append(deep, dir)
		if filepath.Clean(dir) == filepath.Clean(top) {
			break
		}
	}
	for i, j := 0, len(deep)-1; i < j; i, j = i+1, j-1 {
		deep[i], deep[j] = deep[j], deep[i]
	}
	return deep
}

func gitToplevel(workspace string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := runGit(ctx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// isAncestor reports whether base is dir itself or an ancestor of it.
func isAncestor(base, dir string) bool {
	base = filepath.Clean(base)
	dir = filepath.Clean(dir)
	if base == dir {
		return true
	}
	return strings.HasPrefix(dir, base+string(filepath.Separator))
}

func dirLabel(workspace, dir string) string {
	if filepath.Clean(dir) == filepath.Clean(workspace) {
		return "workspace"
	}
	return "parent: " + filepath.Base(dir)
}

func readContextFile(dir, name string, read func(string) ([]byte, error)) []byte {
	if read != nil {
		data, err := read(name)
		if err != nil || len(data) == 0 {
			return nil
		}
		if len(data) > contextCapBytes {
			data = []byte(truncateRunes(string(data), contextCapBytes))
		}
		return data
	}
	data, err := readFileLimited(joinPath(dir, name), contextCapBytes)
	if err != nil || len(data) == 0 {
		return nil
	}
	return data
}

// globalHome is the user-level context directory（测试桩缝）.
var globalHome = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".zgo")
}

// globalContext reads ~/.zgo/ZGO.md（用户全局上下文，对所有工作区——
// 含远程——生效）。
func globalContext() string {
	dir := globalHome()
	if dir == "" {
		return ""
	}
	data, err := readFileLimited(joinPath(dir, "ZGO.md"), contextCapBytes)
	if err != nil || len(data) == 0 {
		return ""
	}
	return string(data)
}

// truncateRunes cuts s to at most limit bytes without splitting a rune.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
