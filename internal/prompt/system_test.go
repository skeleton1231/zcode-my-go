package prompt

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/winpath"
)

func TestSystemIncludesEnvironment(t *testing.T) {
	got := System(t.TempDir())
	for _, want := range []string{"Platform:", "Workspace:", "Shell:", "How to work:"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// TestSystemIncludesProjectContext verifies the first context file wins and
// later ones are ignored.
func TestSystemIncludesProjectContext(t *testing.T) {
	hermeticContext(t)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("# AGENTS\n用中文注释"), 0o644)
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("# CLAUDE"), 0o644)

	got := System(ws)
	if !strings.Contains(got, "用中文注释") {
		t.Fatalf("AGENTS.md context missing:\n%s", got)
	}
	if strings.Contains(got, "# CLAUDE") {
		t.Fatalf("later context file also included:\n%s", got)
	}
}

func TestSystemCapsContext(t *testing.T) {
	hermeticContext(t)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "ZGO.md"), []byte(strings.Repeat("字", 10000)), 0o644)
	got := System(ws)
	// 8KB 上限：10000 个“字”（UTF-8 3 字节）应被截断。
	if strings.Count(got, "字") > 3000 {
		t.Fatalf("context not capped: %d 字", strings.Count(got, "字"))
	}
}

func TestGitSummaryInRealRepo(t *testing.T) {
	ws := t.TempDir()
	if got := gitSummary(ws); got != "" {
		t.Fatalf("non-repo summary = %q", got)
	}
	// 本仓库本身是 git 仓库，摘要应包含分支与状态。
	if got := gitSummary("."); got == "" {
		t.Skip("git not available in this environment")
	} else if !strings.Contains(got, "(") {
		t.Fatalf("summary lacks state: %q", got)
	}
}

// hermeticContext isolates a test from the machine's surroundings: the
// up-walk stops at the temp root（本机 HOME 自身是 git 仓库——不设天花
// 板，临时目录会一路回溯吸进用户真实的 ~/AGENTS.md）and the user-global
// context reads an empty stub home.
func hermeticContext(t *testing.T) string {
	t.Helper()
	stubHome := t.TempDir()
	orig := globalHome
	globalHome = func() string { return stubHome }
	t.Cleanup(func() { globalHome = orig })
	// ceiling 必须长形态：runner 的 os.TempDir() 带 RUNNER~1 短名而 git
	// 按长形态走——短名条目不匹配会被静默忽略，发现 climbs 出 Temp
	//（TestSystemNoGitSingleLayer 曾因此意外撞见上层仓库）。
	t.Setenv("GIT_CEILING_DIRECTORIES", winpath.Long(os.TempDir()))
	return stubHome
}

// TestSystemHierarchyCollectsParents verifies the up-walk: a repo root
// AGENTS.md plus a nested workspace CLAUDE.md both land in the prompt,
// shallow (repo root) before deep (workspace).
func TestSystemHierarchyCollectsParents(t *testing.T) {
	hermeticContext(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("repo-level rules"), 0o644)
	os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("nested specifics"), 0o644)

	got := System(sub)
	if !strings.Contains(got, "repo-level rules") || !strings.Contains(got, "nested specifics") {
		t.Fatalf("hierarchy missing parts:\n%s", got)
	}
	if strings.Index(got, "repo-level rules") > strings.Index(got, "nested specifics") {
		t.Fatal("shallow (repo root) must come before deep (workspace)")
	}
	if !strings.Contains(got, "--- parent: ") || !strings.Contains(got, "--- workspace ---") {
		t.Fatalf("hierarchy labels missing:\n%s", got)
	}
}

// TestSystemNoGitSingleLevel: outside a repo (ceiling blocks the climb to
// any enclosing repo), only the workspace's own context file applies.
func TestSystemNoGitSingleLevel(t *testing.T) {
	hermeticContext(t)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "ZGO.md"), []byte("only this"), 0o644)
	got := System(ws)
	if !strings.Contains(got, "only this") {
		t.Fatalf("workspace context missing:\n%s", got)
	}
	if strings.Contains(got, "parent: ") {
		t.Fatal("unexpected up-walk outside a repo")
	}
}

// TestSystemGlobalUserContext: ~/.zgo/ZGO.md lands as the first part.
func TestSystemGlobalUserContext(t *testing.T) {
	home := hermeticContext(t)
	os.WriteFile(filepath.Join(home, "ZGO.md"), []byte("global habits"), 0o644)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("local rules"), 0o644)
	got := System(ws)
	if !strings.Contains(got, "global habits") || !strings.Contains(got, "local rules") {
		t.Fatalf("missing parts:\n%s", got)
	}
	if strings.Index(got, "global habits") > strings.Index(got, "local rules") {
		t.Fatal("global must come before workspace")
	}
}

// TestSystemFSRemoteReader: a remote reader (root-relative names) feeds
// the workspace part; the prompt then never touches the local fs.
func TestSystemFSRemoteReader(t *testing.T) {
	hermeticContext(t)
	var seen []string
	read := func(name string) ([]byte, error) {
		seen = append(seen, name)
		if name == "AGENTS.md" {
			return []byte("over-the-wire context"), nil
		}
		return nil, os.ErrNotExist
	}
	got := SystemFS("/remote/ws", read)
	if !strings.Contains(got, "over-the-wire context") {
		t.Fatalf("remote context missing:\n%s", got)
	}
	if len(seen) == 0 || seen[0] != "ZGO.md" {
		t.Fatalf("reader got %v, want root-relative names", seen)
	}
}

// TestSystemHierarchyCap: parts summing past the cap get cut with a note.
func TestSystemHierarchyCap(t *testing.T) {
	hermeticContext(t)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "AGENTS.md"),
		[]byte(strings.Repeat("字", 4000)), 0o644)
	got := System(ws)
	if !strings.Contains(got, "context capped at 8KB") {
		t.Fatalf("cap note missing (字 x%d)", strings.Count(got, "字"))
	}
	if strings.Count(got, "字") > 2800 {
		t.Fatalf("context not capped: %d 字", strings.Count(got, "字"))
	}
}

// TestIsAncestorFoldsCaseOnWindows: Windows 卷大小写不敏感——同一目录
// 的不同大小写形态（c:\repo vs C:\Repo）即真祖先；前缀必须落在段边界
// （issue #167：git 返回大写盘符、工作区按用户给定形态存储，大小写不
// 折会让层级静默降级单层）。
func TestIsAncestorFoldsCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive comparison is a Windows-volume behavior")
	}
	if !isAncestor(`C:\Repo`, `c:\repo\sub`) {
		t.Fatal("same dir, different case must be an ancestor")
	}
	if !isAncestor(`c:/repo`, `C:/Repo/sub`) {
		t.Fatal("mixed separators must still fold (Clean 自愈 + 大小写折叠)")
	}
	if isAncestor(`c:\repo`, `c:\repository`) {
		t.Fatal("prefix match must stop at segment boundary")
	}
}

// TestSystemHierarchyCaseFoldedWorkspace: 小写化的工作区路径（Windows
// 同一目录的另一形态）不得让层级降级为单层——repo 级上下文照常注入。
func TestSystemHierarchyCaseFoldedWorkspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive paths are a Windows-volume behavior")
	}
	hermeticContext(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("repo-level rules"), 0o644)
	os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("nested specifics"), 0o644)

	// git show-toplevel 返回规范化形态（大写盘符），工作区整路径小写
	// 化——两个真实形态，此前在 isAncestor 处撞车。
	ws := strings.ToLower(sub)
	got := System(ws)
	if !strings.Contains(got, "repo-level rules") {
		t.Fatalf("lowercased workspace lost repo-level layer:\n%s", got)
	}
	if !strings.Contains(got, "nested specifics") {
		t.Fatalf("workspace layer missing:\n%s", got)
	}
}

// TestSystemHierarchyShortNameWorkspace: 工作区以 8.3 短名传入（CI
// runner 的 TMP 即 RUNNER~1 形态）时层级不得降级单层——git toplevel 返
// 回长形态，两侧须归一后再判祖先（winpath.Long）。
func TestSystemHierarchyShortNameWorkspace(t *testing.T) {
	hermeticContext(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "long-sub", "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("repo-level rules"), 0o644)
	os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("nested specifics"), 0o644)

	short, ok := shortPathOf(t, sub)
	if !ok {
		t.Skip("volume has 8dot3 disabled")
	}
	got := System(short)
	if !strings.Contains(got, "repo-level rules") {
		t.Fatalf("short-name workspace lost repo layer:\n%s", got)
	}
	if !strings.Contains(got, "nested specifics") {
		t.Fatalf("workspace layer missing:\n%s", got)
	}
}
