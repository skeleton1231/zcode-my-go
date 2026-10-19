package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// gitInit makes the harness workspace a repository with one committed
// file, so turns get checkpointed (skipped when git is unavailable — the
// checkpoint package covers the degradation paths).
func gitInit(t *testing.T, dir string) {
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
	if err := os.WriteFile(dir+"/.gitignore", []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/base.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
}

// TestE2ERewindLatestRestoresWorkspace: a turn whose scripted write lands
// on disk is undone whole — RewindLatest truncates the transcript and the
// workspace file (created by the turn) disappears; the base file survives
// untouched.
func TestE2ERewindLatestRestoresWorkspace(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "agent-made.txt", "content": "turn output\n"}),
		}}},
		Turn{Text: "已创建 agent-made.txt"},
	)
	gitInit(t, h.Workspace)

	events := h.Ask("创建 agent-made.txt")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if got := h.ReadWorkspace("agent-made.txt"); !strings.Contains(got, "turn output") {
		t.Fatalf("turn file = %q", got)
	}

	if err := h.Svc.RewindLatest(h.SessionID); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if h.WorkspaceHas("agent-made.txt") {
		t.Fatal("rewound turn's file survived on disk")
	}
	if got := h.ReadWorkspace("base.txt"); got != "base\n" {
		t.Fatalf("base file damaged: %q", got)
	}
	msgs, err := h.Svc.Messages(h.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Text(), "agent-made") {
			t.Fatalf("transcript not rewound: %+v", msgs)
		}
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty transcript after rewinding the only turn, got %d messages", len(msgs))
	}
}

func TestE2ENonGitRewindRestoresFiles(t *testing.T) {
	// 文件快照（spec 规则 83）：非 git 工作区此前降级「仅回退转录、文
	// 件不动」——现在走快照镜像回写，回合内写入的文件随转录一起回退。
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "gone.txt", "content": "gone\n"}),
		}}},
		Turn{Text: "已创建 gone.txt"},
	)
	// 回合前先放一个基础文件——快照里有它，回退后必须还在。
	h.WriteWorkspace("base.txt", "base\n")
	events := h.Ask("创建 gone.txt")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if !h.WorkspaceHas("gone.txt") || !h.WorkspaceHas("base.txt") {
		t.Fatal("setup: files missing before rewind")
	}
	if err := h.Svc.RewindLatest(h.SessionID); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if h.WorkspaceHas("gone.txt") {
		t.Fatal("non-git rewind should remove files written during the turn (snapshot mirror)")
	}
	if !h.WorkspaceHas("base.txt") {
		t.Fatal("snapshot-mirrored base file lost")
	}
	msgs, err := h.Svc.Messages(h.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("transcript not rewound: %d messages", len(msgs))
	}
}
