package service

// 上下文构成观测（spec 规则 79）：分段与下一请求同源、空会话也有系
// 统提示与工具面、有回合后转录段增长。

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestContextBreakdownSegments(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	ws := t.TempDir()
	if err := svc.TrustWorkspace(ws, "test"); err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}

	byName := func(parts []ContextPart, name string) *ContextPart {
		for i := range parts {
			if parts[i].Name == name {
				return &parts[i]
			}
		}
		return nil
	}

	parts := svc.ContextBreakdown(info.ID, ws)
	if parts == nil {
		t.Fatal("nil breakdown")
	}
	if p := byName(parts, "system·stable"); p == nil || p.Bytes < 100 {
		t.Fatalf("stable segment: %+v", p)
	}
	if p := byName(parts, "memory"); p == nil || p.Bytes != 0 {
		t.Fatalf("empty memory should be 0: %+v", p)
	}
	if p := byName(parts, "tools"); p == nil || p.Bytes < 1000 {
		t.Fatalf("tools declaration segment: %+v", p)
	}
	if p := byName(parts, "transcript"); p == nil || p.Bytes != 0 || !strings.Contains(p.Note, "0 条消息") {
		t.Fatalf("empty transcript: %+v", p)
	}

	// 一轮对话后转录段非零。
	if err := svc.AppendForTesting(info.ID, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "一段足够长的用户消息用于观测转录段的增长"}}}); err != nil {
		t.Fatal(err)
	}
	parts = svc.ContextBreakdown(info.ID, ws)
	if p := byName(parts, "transcript"); p == nil || p.Bytes == 0 {
		t.Fatalf("transcript after append: %+v", p)
	}
}
