package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// gitInitRepo makes the workspace a repo with one committed file.
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
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
	os.WriteFile(dir+"/base.txt", []byte("base\n"), 0o644)
	run("add", "-A")
	run("commit", "-qm", "init")
}

// TestE2ERewindToMiddleTurn: two scripted turns; rewinding to turn 1
// drops the second turn's file AND its transcript, while the first
// turn's output stays byte-identical.
func TestE2ERewindToMiddleTurn(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "one.txt", "content": "第一轮\n"}),
		}}},
		Turn{Text: "第一轮完成"},
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "two.txt", "content": "第二轮\n"}),
		}}},
		Turn{Text: "第二轮完成"},
	)
	gitInitRepo(t, h.Workspace)

	h.Ask("第一问")
	events := h.Ask("第二问")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("second run did not end: %+v", events)
	}
	if !h.WorkspaceHas("two.txt") || !h.WorkspaceHas("one.txt") {
		t.Fatal("both turns' files must land first")
	}

	// 回滚到第 2 轮之前：撤掉第 2 轮（two.txt 消失），第 1 轮完好。
	if err := h.Svc.RewindTo(h.SessionID, 2); err != nil {
		t.Fatalf("rewind to turn 2: %v", err)
	}
	if h.WorkspaceHas("two.txt") {
		t.Fatal("turn-2 file survived rewinding to turn 2")
	}
	if got := h.ReadWorkspace("one.txt"); got != "第一轮\n" {
		t.Fatalf("turn-1 file damaged: %q", got)
	}
	msgs, err := h.Svc.Messages(h.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, m := range msgs {
		all += m.Text() + " "
	}
	if strings.Contains(all, "第二问") || strings.Contains(all, "two.txt") {
		t.Fatalf("turn-2 transcript survived: %q", all)
	}
	if !strings.Contains(all, "第一问") {
		t.Fatalf("turn-1 transcript lost when rewinding to turn 2: %q", all)
	}

	// 再回滚到第 1 轮之前：全部撤空，只剩底座。
	if err := h.Svc.RewindTo(h.SessionID, 1); err != nil {
		t.Fatalf("rewind to turn 1: %v", err)
	}
	if h.WorkspaceHas("one.txt") {
		t.Fatal("turn-1 file survived rewinding to turn 1")
	}
	if got := h.ReadWorkspace("base.txt"); got != "base\n" {
		t.Fatalf("base damaged: %q", got)
	}

	// 越界拒绝。
	if err := h.Svc.RewindTo(h.SessionID, 9); err == nil {
		t.Fatal("out-of-range turn accepted")
	}
}

// TestE2ERewindToRefusesWhileRunning: the run guard fires before any
// file restore（issue #110 的任意回合版）.
func TestE2ERewindToRefusesWhileRunning(t *testing.T) {
	h := New(t, Turn{Text: "慢回复", BlockAfterText: true})
	go func() { _ = h.Svc.Chat(h.SessionID, "问", func(agent.Event) {}) }()
	waitRun := func() bool {
		for i := 0; i < 100; i++ {
			if h.Svc.SessionRunning(h.SessionID) {
				return true
			}
			time.Sleep(5 * time.Millisecond)
		}
		return false
	}()
	if !waitRun {
		t.Fatal("run never started")
	}
	h.WriteWorkspace("sentinel.txt", "在跑时不得被动\n")
	if err := h.Svc.RewindTo(h.SessionID, 1); err == nil {
		t.Fatal("rewind accepted during a run")
	}
	h.Svc.Abort(h.SessionID)
}
