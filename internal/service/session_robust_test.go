package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestNonGitWorkspaceFileRewind（spec 规则 83）：非 git 工作区的回合
// 回滚走文件快照——文件与转录一起回（此前只剩仅回退转录的降级）。
func TestNonGitWorkspaceFileRewind(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir() // 无 .git
	if err := os.WriteFile(filepath.Join(ws, "base.txt"), []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	p := &seqScriptProvider{turns: []string{"第一答", "第二答"}}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "问题一", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// 回合内改文件（工具路径等效：直接写工作区）。
	if err := os.WriteFile(filepath.Join(ws, "base.txt"), []byte("v1-turn1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "问题二", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// 回滚最近一回合：文件回到回合开始前（v1-turn1——第二回合的快照
	// 拍在 v1-turn1 之后？不——快照拍在回合开始前=第二回合开始时文件
	// 已是 v1-turn1；回滚撤掉第二回合恢复到 v1-turn1。
	if err := svc.RewindLatest(info.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(ws, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "v1-turn1" {
		t.Fatalf("file rewind broken: %q", string(raw))
	}
	// 再回滚一回合（RewindTo(1) 前即撤掉第一回合）：回到 v0。
	if err := svc.RewindTo(info.ID, 1); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(ws, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "v0" {
		t.Fatalf("second rewind broken: %q", string(raw))
	}
}

// seqScriptProvider 按序回放固定文本（auto-memory 的 memScriptProvider
// 是回合计数驱动的标题形态——这里按序号取）。
type seqScriptProvider struct {
	mu    int
	turns []string
}

func (p *seqScriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu++
	text := p.turns[(p.mu-1)%len(p.turns)]
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: text}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}
