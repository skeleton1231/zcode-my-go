package service

// 会话谱系测试（spec 规则 103）：fork 落父、树组装（孤儿当根、环防
// 御交给 attach 的 seen）、归档过滤、meta 持久化重载带回。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func lineageSvc(t *testing.T) (*AgentService, string) {
	t.Helper()
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.UseProviderForTesting(func() ai.Provider { return &plainTurnProvider{} })
	return svc, info.ID
}

// plainTurnProvider 每回合一条用户消息一条助手回复（凑够 fork 点）。
type plainTurnProvider struct{}

func (p *plainTurnProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "回复"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func TestSessionLineage(t *testing.T) {
	svc, id := lineageSvc(t)
	if err := svc.Chat(id, "第一问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(id, "第二问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	fork, err := svc.ForkSession(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if fork.ParentID != id {
		t.Fatalf("fork parent = %q want %q", fork.ParentID, id)
	}
	// 孙代。
	if err := svc.Chat(fork.ID, "分叉后一问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	grand, err := svc.ForkSession(fork.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if grand.ParentID != fork.ID {
		t.Fatalf("grand parent = %q", grand.ParentID)
	}

	roots := svc.SessionTree(false)
	if len(roots) != 1 {
		t.Fatalf("roots = %d（一条根链）", len(roots))
	}
	root := roots[0]
	if root.Info.ID != id || len(root.Children) != 1 || root.Children[0].Info.ID != fork.ID {
		t.Fatalf("tree shape wrong: root=%s children=%d", root.Info.ID, len(root.Children))
	}
	if len(root.Children[0].Children) != 1 || root.Children[0].Children[0].Info.ID != grand.ID {
		t.Fatal("grandchild not nested")
	}

	// meta 持久化：重载后 ParentID 仍在（重启语义）。
	svc2, err := New(filepath.Dir(svc.path))
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	roots2 := svc2.SessionTree(false)
	if len(roots2) != 1 || len(roots2[0].Children) != 1 {
		t.Fatalf("lineage lost across restart: %+v", roots2)
	}

	// 归档过滤：分叉归档后从树里消失（孙代成孤儿根）、--all 回来。
	if err := svc.SetSessionArchived(fork.ID, true); err != nil {
		t.Fatal(err)
	}
	roots = svc.SessionTree(false)
	byID := map[string]*agent.TreeNode{}
	for _, r := range roots {
		byID[r.Info.ID] = r
	}
	if byID[id] == nil || byID[grand.ID] == nil || byID[fork.ID] != nil {
		t.Fatalf("archived filter wrong: roots=%d forkVisible=%v", len(roots), byID[fork.ID] != nil)
	}
	if roots := svc.SessionTree(true); len(roots) == 0 {
		t.Fatal("--all lost everything")
	}
}
