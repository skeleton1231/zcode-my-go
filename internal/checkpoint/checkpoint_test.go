package checkpoint

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a real repository with one committed file and one ignored
// file. Tests skip when git is unavailable (conversation-only rewind
// remains covered by the service layer).
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=zgo", "GIT_AUTHOR_EMAIL=zgo@test",
			"GIT_COMMITTER_NAME=zgo", "GIT_COMMITTER_EMAIL=zgo@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	write(t, dir, ".gitignore", "ignored.txt\n")
	write(t, dir, "base.txt", "base\n")
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "<missing:" + err.Error() + ">"
	}
	return string(data)
}

// TestCaptureRestoreRoundTrip: a checkpoint taken before a turn survives
// every kind of change that turn can make — edited tracked file, new
// untracked file, deleted file, and bash-made junk — while ignored files
// are never touched and history never moves.
func TestCaptureRestoreRoundTrip(t *testing.T) {
	dir := gitRepo(t)
	headBefore, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()

	if err := Capture(dir, "s1", 0); err != nil {
		t.Fatalf("capture: %v", err)
	}
	// 回合改动：改、增、删 + 事后垃圾。
	write(t, dir, "base.txt", "changed\n")
	write(t, dir, "new.txt", "created by agent\n")
	if err := os.Remove(filepath.Join(dir, "base.txt")); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "new.txt"))
	write(t, dir, "junk.txt", "after checkpoint\n")

	if err := Restore(dir, "s1", 0); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := read(t, dir, "base.txt"); got != "base\n" {
		t.Fatalf("base.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk.txt")); !os.IsNotExist(err) {
		t.Fatal("post-checkpoint file survived restore")
	}
	headAfter, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if string(headAfter) != string(headBefore) {
		t.Fatal("restore moved HEAD/branch")
	}
}

// TestCaptureKeepsIgnoredAndUntracked: the snapshot includes untracked
// files (agent-created) but not ignored ones; restore brings untracked
// back and never deletes ignored.
func TestCaptureKeepsIgnoredAndUntracked(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "ignored.txt", "local secret\n")
	write(t, dir, "draft.md", "untracked draft\n")
	if err := Capture(dir, "s1", 0); err != nil {
		t.Fatalf("capture: %v", err)
	}
	os.Remove(filepath.Join(dir, "draft.md"))

	if err := Restore(dir, "s1", 0); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := read(t, dir, "draft.md"); got != "untracked draft\n" {
		t.Fatalf("untracked file not restored: %q", got)
	}
	// ignored 不入检查点：restore/clean 不删它，但也不会复原它。
	if got := read(t, dir, "ignored.txt"); got != "local secret\n" {
		t.Fatalf("ignored file removed by restore: %q", got)
	}
}

func TestNotARepoAndMissingCheckpoint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	plain := t.TempDir()
	if err := Capture(plain, "s1", 0); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("plain dir capture err = %v", err)
	}
	dir := gitRepo(t)
	if err := Capture(dir, "s1", 0); err != nil {
		t.Fatal(err)
	}
	if err := Restore(dir, "s1", 5); err == nil || !errors.Is(err, ErrCheckpointPruned) {
		t.Fatalf("missing checkpoint err = %v", err)
	}
	// 无 HEAD 的空仓库也能捕获（全部按未跟踪收录）。
	empty := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", empty}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	write(t, empty, "x.txt", "x\n")
	if err := Capture(empty, "s2", 0); err != nil {
		t.Fatalf("empty repo capture: %v", err)
	}
	if err := Restore(empty, "s2", 0); err != nil {
		t.Fatalf("empty repo restore: %v", err)
	}
	if got := read(t, empty, "x.txt"); got != "x\n" {
		t.Fatalf("x.txt = %q", got)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := gitRepo(t)
	for seq := 0; seq < Retention+2; seq++ {
		write(t, dir, "f.txt", string(rune('a'+seq%26))+"\n")
		if err := Capture(dir, "s1", seq); err != nil {
			t.Fatalf("capture %d: %v", seq, err)
		}
	}
	out, err := exec.Command("git", "-C", dir, "for-each-ref",
		"--format=%(refname)", "refs/zgo-checkpoints/s1/").Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != Retention {
		t.Fatalf("kept %d refs, want %d", len(lines), Retention)
	}
	if strings.Contains(string(out), "/s1/0\n") || strings.Contains(string(out), "/s1/1\n") {
		t.Fatal("oldest checkpoints not pruned")
	}
	// 新的仍在。
	if err := Restore(dir, "s1", Retention+1); err != nil {
		t.Fatalf("newest pruned: %v", err)
	}
}

// TestRestoreDistinguishesPrunedFromNonRepo: 仓库里被保留窗口修剪的 ref
// 与非仓库降级必须可区分（issue #152：此前共用 ErrNoCheckpoint 哨兵，
// service 把危险修剪当良性降级——文件静默不回退、转录照常回退）。
func TestRestoreDistinguishesPrunedFromNonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	plain := t.TempDir()
	if err := Restore(plain, "s1", 0); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("plain dir restore err = %v, want ErrNotARepo", err)
	}
	dir := gitRepo(t)
	if err := Capture(dir, "s9", 0); err != nil {
		t.Fatal(err)
	}
	if err := Prune(dir, "s9", 0); err != nil { // 保留窗口清零——旧 ref 修剪
		t.Fatal(err)
	}
	if err := Restore(dir, "s9", 0); !errors.Is(err, ErrCheckpointPruned) {
		t.Fatalf("pruned restore err = %v, want ErrCheckpointPruned", err)
	}
}

// TestRemoveAllDropsEveryRef: 删除会话必须清光其检查点 refs（issue
// #224：活 ref 永久钉住 git 对象——非回收的硬泄漏）；非仓库目录静默
// 无操作。
func TestRemoveAllDropsEveryRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := gitRepo(t)
	for seq := 0; seq < 3; seq++ {
		write(t, dir, "f.txt", string(rune('a'+seq))+"\n")
		if err := Capture(dir, "s1", seq); err != nil {
			t.Fatalf("capture %d: %v", seq, err)
		}
	}
	if err := Capture(dir, "other", 0); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(dir, "s1"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "for-each-ref",
		"--format=%(refname)", "refs/zgo-checkpoints/s1/").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("s1 refs survived: %s", out)
	}
	// 其他会话的 refs 不受影响。
	out2, _ := exec.Command("git", "-C", dir, "for-each-ref",
		"--format=%(refname)", "refs/zgo-checkpoints/other/").Output()
	if !strings.Contains(string(out2), "refs/zgo-checkpoints/other/0") {
		t.Fatalf("other session refs collateral damage: %s", out2)
	}
	// 非仓库目录：静默无操作（删除路径不强求仓库）。
	if err := RemoveAll(t.TempDir(), "s1"); err != nil {
		t.Fatalf("RemoveAll on non-repo: %v", err)
	}
}

// TestCaptureRestoreViaShortPath: 工作区以 8.3 短名形态传入（CI runner
// 的 TMP 即 RUNNER~1 形态）时，git toplevel 的长形态对比不得误判非仓库
//（winpath.Long 归一——CI 首跑 windows 上检查点全军覆没的根因）。
func TestCaptureRestoreViaShortPath(t *testing.T) {
	short, ok := shortPathOf(t, gitRepo(t))
	if !ok {
		t.Skip("volume has 8dot3 disabled")
	}
	write(t, short, "s.txt", "v1\n")
	if err := Capture(short, "s1", 0); err != nil {
		t.Fatalf("capture via short path: %v", err)
	}
	write(t, short, "s.txt", "v2\n")
	if err := Restore(short, "s1", 0); err != nil {
		t.Fatalf("restore via short path: %v", err)
	}
	if got := read(t, short, "s.txt"); got != "v1\n" {
		t.Fatalf("s.txt = %q", got)
	}
}
