package service

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func seedSession(t *testing.T, svc *AgentService, ws, text string) string {
	t.Helper()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AppendForTesting(info.ID, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}}); err != nil {
		t.Fatal(err)
	}
	return info.ID
}

// TestSearchSessionsFindsAcrossFiles: hits across sessions carry the
// snippet and the session id; caps hold (per-session and total).
func TestSearchSessionsFindsAcrossFiles(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()

	a := seedSession(t, svc, ws, "修一下 login 页面的样式问题")
	seedSession(t, svc, ws, "无关内容")
	b := seedSession(t, svc, ws, "另一次 login 相关的排查")

	hits := svc.SearchSessions("login")
	if len(hits) < 2 {
		t.Fatalf("hits = %+v", hits)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.SessionID] = true
		if !strings.Contains(strings.ToLower(h.Snippet), "login") {
			t.Fatalf("snippet = %q", h.Snippet)
		}
	}
	if !ids[a] || !ids[b] {
		t.Fatalf("missing sessions: %+v", ids)
	}

	if got := svc.SearchSessions("不存在的关键词xyz"); len(got) != 0 {
		t.Fatalf("no-hit search returned %+v", got)
	}
	if got := svc.SearchSessions("   "); len(got) != 0 {
		t.Fatalf("blank query returned %+v", got)
	}
}

// TestSearchSessionsMatchesToolArgs: tool-call arguments participate —
// the file being edited is often only in args.
func TestSearchSessionsMatchesToolArgs(t *testing.T) {
	svc, _ := New(t.TempDir())
	t.Cleanup(svc.Close)
	id := seedSession(t, svc, t.TempDir(), "开始修改")
	if err := svc.AppendForTesting(id, ai.Message{Role: ai.RoleAssistant,
		Blocks: []ai.Block{{Kind: ai.BlockToolCall, Name: "edit",
			Arguments: `{"path":"src/payments/checkout.go"}`}}}); err != nil {
		t.Fatal(err)
	}
	hits := svc.SearchSessions("checkout.go")
	if len(hits) != 1 || hits[0].SessionID != id {
		t.Fatalf("hits = %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "checkout") {
		t.Fatalf("snippet = %q", hits[0].Snippet)
	}
}
