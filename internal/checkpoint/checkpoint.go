// Package checkpoint captures and restores workspace trees (ZCode's
// conversation-branch-rewind capability, the file-rewind half): every user
// turn starts from a snapshot in the workspace's own git object store, and
// rewinding a turn puts the files back exactly as they were — covering
// every change source, bash included, because it is the whole tree.
//
// Semantics: capture never touches the worktree or the real index (a
// temporary GIT_INDEX_FILE holds the staging), and restore never moves a
// branch or rewrites history (read-tree -u --reset + clean -fd; ignored
// files are never removed).
package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Retention is how many checkpoints a session keeps (newest by turn).
const Retention = 20

var (
	// ErrNotARepo: the directory is not a git repository (or git is
	// missing) — callers degrade to conversation-only rewind.
	ErrNotARepo = errors.New("not a git repository")
	// ErrCheckpointPruned: the repo is real but the turn's checkpoint ref
	// is gone (retention window or predates checkpointing). 仓库内不可降
	// 级为仅回退转录（issue #152：与 ErrNotARepo 曾共用哨兵，静默半回
	// 退让模型拿旧假设改新文件）——调用方必须中止并告知用户。
	ErrCheckpointPruned = errors.New("checkpoint pruned (outside retention window)")
)

func refName(session string, seq int) string {
	return fmt.Sprintf("refs/zgo-checkpoints/%s/%d", session, seq)
}

// baseArgs prefixes every git call: no CRLF conversion (a checkpoint must
// restore the exact bytes the turn started from — autocrlf would rewrite
// them) and an explicit -C.
func baseArgs(dir string, args ...string) []string {
	return append([]string{"-c", "core.autocrlf=false", "-c", "core.eof=lf",
		"-c", "core.safecrlf=false", "-C", dir}, args...)
}

// isRepoRoot reports whether dir is itself a git repository root. Discovery
// is not enough: a workspace inside a parent repo (or under a home that is
// one) would checkpoint into that parent — refs and objects leaking out of
// the workspace. Only a workspace that is its own toplevel qualifies.
func isRepoRoot(git, dir string) (bool, error) {
	out, err := exec.Command(git, baseArgs(dir, "rev-parse", "--show-toplevel")...).Output()
	if err != nil {
		return false, nil // not a repo at all（含 git 报错，一律降级）
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(filepath.Clean(abs), top), nil
}

// Capture records the workspace's current tree (tracked changes plus
// untracked files, not ignored) as the session's checkpoint for turn seq,
// without touching the worktree or the real index. It prunes older
// checkpoints beyond Retention.
func Capture(dir, session string, seq int) error {
	git, err := gitCmd()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "zgo-index-*")
	if err != nil {
		return fmt.Errorf("temp index: %w", err)
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	if ok, err := isRepoRoot(git, dir); err != nil || !ok {
		return fmt.Errorf("%w", ErrNotARepo)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+tmp.Name())
	run := func(args ...string) (string, error) {
		cmd := exec.Command(git, baseArgs(dir, args...)...)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			detail := strings.TrimSpace(string(out))
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				detail = strings.TrimSpace(string(ee.Stderr))
			}
			return "", fmt.Errorf("git %s: %s", args[0], detail)
		}
		return strings.TrimSpace(string(out)), nil
	}

	// 空仓库没有 HEAD：从空索引开始照样能捕获（全部按未跟踪收录）。
	if _, err := run("read-tree", "HEAD"); err != nil {
		if _, err := run("read-tree", "--empty"); err != nil {
			return err
		}
	}
	if _, err := run("add", "-A", "."); err != nil {
		return err
	}
	tree, err := run("write-tree")
	if err != nil {
		return err
	}
	commit, err := run("commit-tree", tree, "-m", "zgo checkpoint "+session+"/"+strconv.Itoa(seq))
	if err != nil {
		return err
	}
	if _, err := run("update-ref", refName(session, seq), commit); err != nil {
		return err
	}
	return Prune(dir, session, Retention)
}

// Restore resets index and worktree to the session's checkpoint for turn
// seq. Untracked files that appeared after the checkpoint are removed;
// ignored files are kept; the branch and history never move.
func Restore(dir, session string, seq int) error {
	git, err := gitCmd()
	if err != nil {
		return err
	}
	run := func(args ...string) error {
		cmd := exec.Command(git, baseArgs(dir, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
		}
		return nil
	}
	if ok, err := isRepoRoot(git, dir); err != nil || !ok {
		return fmt.Errorf("%w", ErrNotARepo)
	}
	ref := refName(session, seq)
	verify := exec.Command(git, baseArgs(dir, "rev-parse", "--verify", ref)...)
	if err := verify.Run(); err != nil {
		return fmt.Errorf("%w (%s)", ErrCheckpointPruned, ref)
	}
	if err := run("read-tree", "-u", "--reset", ref); err != nil {
		return err
	}
	return run("clean", "-fd")
}

// Prune keeps only the newest `keep` checkpoints of the session.
func Prune(dir, session string, keep int) error {
	git, err := gitCmd()
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("refs/zgo-checkpoints/%s/", session)
	out, err := exec.Command(git, baseArgs(dir, "for-each-ref", "--format=%(refname)", prefix)...).Output()
	if err != nil {
		return fmt.Errorf("git for-each-ref: %w", err)
	}
	var seqs []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), prefix))
		if err != nil {
			continue
		}
		seqs = append(seqs, n)
	}
	sort.Ints(seqs)
	for _, n := range seqs[:max(0, len(seqs)-keep)] {
		cmd := exec.Command(git, baseArgs(dir, "update-ref", "-d", prefix+strconv.Itoa(n))...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git update-ref -d: %s", strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// gitCmd resolves the git binary once; missing git is ErrNotARepo
// (conversation-only rewind still works).
func gitCmd() (string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("%w (git not found)", ErrNotARepo)
	}
	return git, nil
}
