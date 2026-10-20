package service

// @git 引用测试（spec 规则 124）：show/范围 diff 展开、未知 rev 与非
// git 工作区原样保留、去重。真实跑 git 子进程（CI 两平台自带 git）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepoEnv 在临时目录建两提交的 git 仓库并开会话。
func gitRepoEnv(t *testing.T) (svc *AgentService, sessionID, ws string) {
	t.Helper()
	ws = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		// 固定身份：临时仓库不受全局/系统 git 配置影响（CI 干净环境也
		// 能提交）。
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for name, content := range map[string]string{"a.txt": "first\n", "b.txt": "hello\n"} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "first commit")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("first\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "second commit")

	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	return svc, info.ID, ws
}

func TestGitMentionShow(t *testing.T) {
	svc, id, _ := gitRepoEnv(t)
	text, _ := svc.expandMentions(id, "看看 @git:HEAD", nil)
	for _, want := range []string{
		"@git:HEAD（提交/差异）：", "second commit", "diff --git", "a.txt",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}

func TestGitMentionRange(t *testing.T) {
	svc, id, _ := gitRepoEnv(t)
	text, _ := svc.expandMentions(id, "对比 @git:HEAD~1..HEAD", nil)
	if !strings.Contains(text, "@git:HEAD~1..HEAD（提交/差异）：") {
		t.Fatalf("header missing:\n%s", text)
	}
	// diff --stat：文件清单 + 增删行统计。
	if !strings.Contains(text, "a.txt") || !strings.Contains(text, "|") {
		t.Fatalf("stat missing:\n%s", text)
	}
	// 范围走 diff（无 commit 消息头）。
	if strings.Contains(text, "second commit") {
		t.Fatalf("range should diff, not show:\n%s", text)
	}
}

// TestGitMentionUnknownRev：未知 rev 原样保留（模型可见，可自行纠正）。
func TestGitMentionUnknownRev(t *testing.T) {
	svc, id, _ := gitRepoEnv(t)
	text, _ := svc.expandMentions(id, "看 @git:nosuchrev", nil)
	if strings.Contains(text, "（提交/差异）") {
		t.Fatalf("unknown rev expanded:\n%s", text)
	}
	if !strings.Contains(text, "@git:nosuchrev") {
		t.Fatal("token not preserved")
	}
}

// TestGitMentionNonGitWorkspace：非 git 工作区同样原样保留。
func TestGitMentionNonGitWorkspace(t *testing.T) {
	svc, id, _ := mentionEnv(t) // 普通 TempDir，无 .git
	text, _ := svc.expandMentions(id, "看 @git:HEAD", nil)
	if strings.Contains(text, "（提交/差异）") || !strings.Contains(text, "@git:HEAD") {
		t.Fatalf("non-git expansion: %s", text)
	}
}

// TestGitMentionDedup：同 rev 多次引用只展开一次。
func TestGitMentionDedup(t *testing.T) {
	svc, id, _ := gitRepoEnv(t)
	text, _ := svc.expandMentions(id, "先看 @git:HEAD 再看 @git:HEAD", nil)
	if n := strings.Count(text, "（提交/差异）："); n != 1 {
		t.Fatalf("expansions = %d:\n%s", n, text)
	}
}
