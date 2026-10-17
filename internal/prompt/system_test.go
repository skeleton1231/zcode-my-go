package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
