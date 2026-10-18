package service

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// writeJSONL appends raw persistence records to a session file — 复刻
// regenerate/compaction 记录的落盘形态（重放态与原始文件的差异面，
// issue #141）。
func writeJSONL(t *testing.T, svc *AgentService, id string, lines ...string) {
	t.Helper()
	path := filepath.Join(svc.dir(), "sessions", id+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// msgLine builds a persisted message record line.
func msgLine(role, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type":    "message",
		"message": ai.Message{Role: ai.Role(role), Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}},
	})
	return string(b)
}

// TestSearchSkipsRegeneratedContent: 重试/编辑重发截断掉的消息仍躺在
// JSONL 里，但重放后不存在——命中即"点进去找不到"（issue #141）。被
// 丢弃的尝试不得出现；截断后追加的新消息照常命中。
func TestSearchSkipsRegeneratedContent(t *testing.T) {
	svc, _ := New(t.TempDir())
	t.Cleanup(svc.Close)
	id := seedSession(t, svc, t.TempDir(), "第一问")
	writeJSONL(t, svc, id,
		msgLine("user", "第一问"),
		msgLine("assistant", "旧的失败回答 ghost-answer-old"),
		`{"type":"regenerate","kept":1}`,
		msgLine("assistant", "新的最终回答 final-answer-new"),
	)
	if hits := svc.SearchSessions("ghost-answer-old"); len(hits) != 0 {
		t.Fatalf("regenerated-away content hit: %+v", hits)
	}
	hits := svc.SearchSessions("final-answer-new")
	if len(hits) != 1 || hits[0].SessionID != id {
		t.Fatalf("post-regenerate content missing: %+v", hits)
	}
}

// TestSearchSkipsCompactedPrefix: 压缩把旧前缀换成摘要——旧消息不得命
// 中；摘要文本是重放态的一部分（合成消息），照常命中。
func TestSearchSkipsCompactedPrefix(t *testing.T) {
	svc, _ := New(t.TempDir())
	t.Cleanup(svc.Close)
	id := seedSession(t, svc, t.TempDir(), "很早的问题")
	writeJSONL(t, svc, id,
		msgLine("user", "很早的问题"),
		msgLine("assistant", "被压缩吞掉的旧回答 compacted-away-old"),
		msgLine("user", "后来的追问"),
		msgLine("assistant", "后来的回答"),
		// kept=2：保留最后两条（追问+回答），前缀换成摘要——旧问答死亡。
		`{"type":"compaction","summary":"早期讨论的摘要 digest-of-old","kept":2}`,
		msgLine("user", "压缩后的新问题"),
	)
	if hits := svc.SearchSessions("compacted-away-old"); len(hits) != 0 {
		t.Fatalf("compacted-away content hit: %+v", hits)
	}
	if hits := svc.SearchSessions("digest-of-old"); len(hits) != 1 {
		t.Fatalf("summary (replay state) not searchable: %+v", hits)
	}
	if hits := svc.SearchSessions("压缩后的新问题"); len(hits) != 1 {
		t.Fatalf("post-compaction content missing: %+v", hits)
	}
}
