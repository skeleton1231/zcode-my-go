package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/sessionidx"
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

// TestSearchIndexedMatchesLiveScan（spec 规则 71 的等价锚点）：同一转
// 录，索引路径与 live 扫描路径的命中（含片段）必须逐字节一致——含
// toolcall 参数命中、regenerate/compaction 重放语义、多次命中取前 N。
func TestSearchIndexedMatchesLiveScan(t *testing.T) {
	svc, _ := New(t.TempDir())
	t.Cleanup(svc.Close)
	id := seedSession(t, svc, t.TempDir(), "等价锚点会话")
	writeJSONL(t, svc, id,
		msgLine("user", "等价锚点会话"),
		msgLine("assistant", "谈及 deploy 的旧回答 parity-deploy-dead"),
		msgLine("assistant", `{"blocks":[{"type":"toolCall","name":"bash","arguments":"{\"cmd\":\"deploy --dry-run\"}"}],"role":"assistant"}`),
		`{"type":"regenerate","kept":2}`,
		msgLine("assistant", "新的 deploy 结论 parity-deploy-live 之一"),
		msgLine("assistant", "新的 deploy 结论 parity-deploy-live 之二"),
		`{"type":"compaction","summary":"历史里 deploy 的摘要 parity-deploy-syn","kept":2}`,
		msgLine("assistant", "压缩后 deploy 的回答 parity-deploy-final"),
	)
	for _, q := range []string{"parity-deploy", "dry-run", "parity-deploy-syn", "ghost"} {
		qq := strings.ToLower(q)
		indexed := matchSegments(mustExtract(t, svc, id), qq, "title")
		live := scanSession(jsonlPath(t, svc, id), substrMatcher(qq), "title", true)
		if len(indexed) != len(live) {
			t.Fatalf("q=%q: indexed %d vs live %d (%+v vs %+v)", q, len(indexed), len(live), indexed, live)
		}
		for i := range indexed {
			if indexed[i].Snippet != live[i].Snippet {
				t.Fatalf("q=%q hit %d snippet mismatch:\n idx=%q\nlive=%q", q, i, indexed[i].Snippet, live[i].Snippet)
			}
		}
	}
}

// TestSearchIndexIncrementalAcrossQueries：第二次查询零文件重读
// （(size, mtime) 保鲜）；追加后增量重抽且新旧内容都可命中。
func TestSearchIndexIncrementalAcrossQueries(t *testing.T) {
	dir := t.TempDir()
	svc, _ := New(dir)
	t.Cleanup(svc.Close)
	id := seedSession(t, svc, t.TempDir(), "增量会话 alpha-marker")
	if hits := svc.SearchSessions("alpha-marker"); len(hits) != 1 {
		t.Fatalf("first query hits = %+v", hits)
	}
	reads := svc.searchIdx.Reads()
	if hits := svc.SearchSessions("alpha-marker"); len(hits) != 1 {
		t.Fatalf("second query hits = %+v", hits)
	}
	if svc.searchIdx.Reads() != reads {
		t.Fatalf("unchanged re-query re-read files: %d -> %d", reads, svc.searchIdx.Reads())
	}
	// 追加：增量重抽，新旧都在。
	svc.AppendForTesting(id, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "追加了 beta-marker"}}})
	if hits := svc.SearchSessions("beta-marker"); len(hits) != 1 {
		t.Fatalf("appended content missing: %+v", hits)
	}
	if hits := svc.SearchSessions("alpha-marker"); len(hits) != 1 {
		t.Fatalf("old content lost after append: %+v", hits)
	}
	if svc.searchIdx.Reads() != reads+1 {
		t.Fatalf("append should re-read exactly once: %d -> %d", reads, svc.searchIdx.Reads())
	}
}

// TestSearchIndexSurvivesRestart：新服务实例同 dataDir 载入快照，第
// 一次查询即零重读（索引持久化的意义所在）。
func TestSearchIndexSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	svc1, _ := New(dir)
	id := seedSession(t, svc1, t.TempDir(), "重启存活会话 restart-marker")
	if hits := svc1.SearchSessions("restart-marker"); len(hits) != 1 {
		t.Fatalf("seed query hits = %+v", hits)
	}
	// 生产路径的落盘是异步的（查询不等写盘）——测试里显式检查点，
	// 免去与重启的竞态。
	if err := svc1.searchIdx.Save(); err != nil {
		t.Fatal(err)
	}
	svc1.Close()

	svc2, _ := New(dir)
	t.Cleanup(svc2.Close)
	if hits := svc2.SearchSessions("restart-marker"); len(hits) != 1 || hits[0].SessionID != id {
		t.Fatalf("restart query hits = %+v (want session %s)", hits, id)
	}
	if svc2.searchIdx.Reads() != 0 {
		t.Fatalf("restart re-read files: %d (want 0 — snapshot should be fresh)", svc2.searchIdx.Reads())
	}
}

// mustExtract/jsonlPath 是等价断言的取材帮手。
func mustExtract(t *testing.T, svc *AgentService, id string) []sessionidx.Segment {
	t.Helper()
	segs, over, err := sessionidx.Extract(jsonlPath(t, svc, id))
	if err != nil || over {
		t.Fatalf("extract: %v over=%v", err, over)
	}
	return segs
}

func jsonlPath(t *testing.T, svc *AgentService, id string) string {
	t.Helper()
	return filepath.Join(svc.dir(), "sessions", id+".jsonl")
}

// TestSearchSessionsRegex（spec 规则 80）：正则命中（RE2、原大小写——
// 大写模式只匹配大写原文；(?i) 忽略）、子串模式不受影响、坏模式报错。
func TestSearchSessionsRegex(t *testing.T) {
	svc, _ := New(t.TempDir())
	t.Cleanup(svc.Close)
	id := seedSession(t, svc, t.TempDir(), "部署 deploy-2024-10 与 Deploy-2025-01 两次")
	svc.AppendForTesting(id, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "回滚 deploy-2024-10-01 那次"}}})

	// 数字后缀模式：两处命中（大小写混合原文里只匹配小写形态）。
	hits, err := svc.SearchSessionsRegex(`deploy-[0-9]{4}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Snippet, "deploy-") {
		t.Fatalf("regex hits = %+v", hits)
	}
	// 原大小写：大写 Deploy 只有显式 (?i) 或大写模式才命中。
	hits, err = svc.SearchSessionsRegex(`Deploy-[0-9]{4}`)
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Snippet, "Deploy-2025") {
		t.Fatalf("case-sensitive regex: %+v %v", hits, err)
	}
	// (?i) 忽略大小写。
	hits, err = svc.SearchSessionsRegex(`(?i)deploy-[0-9]{4}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("(?i) regex no hits")
	}
	// 坏模式：错误透出。
	if _, err := svc.SearchSessionsRegex(`deploy-[`); err == nil {
		t.Fatal("bad pattern accepted")
	}
	// 子串形态不受影响（回归）。
	if got := svc.SearchSessions("deploy-2024"); len(got) == 0 {
		t.Fatal("substring search regressed")
	}
}
