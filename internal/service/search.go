package service

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// 跨会话搜索（ZCode task-index 的轻量落地）：流式扫 JSONL，不整态装载。
const (
	searchHitCap       = 30  // 总命中上限
	searchHitPerSess   = 3   // 每会话命中上限
	searchSnippetWidth = 80  // 命中前后各取的字符数
	searchCandidateCap = 256 // 每会话暂存命中上限（防御性；存活过滤前）
)

// SearchHit is one match in one session.
type SearchHit struct {
	SessionID string
	Title     string
	Snippet   string // 命中处的上下文片段
}

// SearchSessions scans every session's transcript (streamed, not loaded)
// for the query, case-insensitive, newest sessions first. Only message
// text participates — meta/usage records stay out.
func (s *AgentService) SearchSessions(query string) []SearchHit {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	sessions := s.mgr.List() // newest first
	hits := make([]SearchHit, 0, searchHitCap)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, info := range sessions {
		// len(hits) 必须持锁读：命中在 worker goroutine 里追加（-race
		// 在 CI 上逮过裸读与持锁写的竞态）。
		mu.Lock()
		full := len(hits) >= searchHitCap
		mu.Unlock()
		if full {
			break
		}
		wg.Add(1)
		go func(info agent.SessionInfo) {
			defer wg.Done()
			found := scanSession(filepath.Join(s.dir(), "sessions", info.ID+".jsonl"), query, info.Title)
			if len(found) == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if len(hits) >= searchHitCap {
				return
			}
			room := searchHitPerSess
			if left := searchHitCap - len(hits); left < room {
				room = left
			}
			if room > len(found) {
				room = len(found)
			}
			for i := 0; i < room; i++ {
				found[i].SessionID = info.ID
				hits = append(hits, found[i])
			}
		}(info)
	}
	wg.Wait()
	return hits
}

// scanSession streams one JSONL file, extracting message text blocks and
// matching; results keep the session title for display. 重放态感知（issue
// #141）：regenerate/compaction 记录丢弃此前的消息，但原始行仍躺在文
// 件里——直接匹配会命中"点进会话找不到"的内容。扫描时同步维护重放
// 存活集（只记序号、不装消息体，维持流式）：命中先暂存，读完整文件
// 后按最终存活集过滤。
func scanSession(path, query, title string) []SearchHit {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	type candidate struct {
		ordinal int // 文件内消息序号；负数 = 合成消息（compaction 摘要）
		hit     SearchHit
	}
	var candidates []candidate
	alive := []int{} // 逻辑消息列表（含合成项）当前存活者（按重放语义）
	next, nextSyn := 0, -1
	r := bufio.NewReaderSize(f, 256*1024)
	for {
		raw, rerr := r.ReadString('\n')
		if line := strings.TrimSpace(raw); line != "" && len(line) <= 16<<20 {
			var rec struct {
				Type    string     `json:"type"`
				Message ai.Message `json:"message"`
				Summary string     `json:"summary"`
				Kept    int        `json:"kept"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil {
				switch rec.Type {
				case "message":
					ord := next
					next++
					alive = append(alive, ord)
					if snippet, ok := matchSnippet(rec.Message, query); ok &&
						len(candidates) < searchCandidateCap {
						candidates = append(candidates, candidate{ord,
							SearchHit{Title: title, Snippet: snippet}})
					}
				case "regenerate":
					// 与 session 重放同判：负值/越界属损坏记录，跳过。
					if rec.Kept >= 0 && rec.Kept <= len(alive) {
						alive = alive[:rec.Kept]
					}
				case "compaction":
					// 与 session 重放同判：空摘要/负 Kept 跳过；前缀换成
					// 摘要合成消息（其文本是重放态的一部分，可命中）。
					if rec.Summary != "" && rec.Kept >= 0 {
						kept := rec.Kept
						if kept > len(alive) {
							kept = len(alive)
						}
						syn := nextSyn
						nextSyn--
						if snippet, ok := matchSnippet(ai.Message{
							Role:   ai.RoleAssistant,
							Blocks: []ai.Block{{Kind: ai.BlockText, Text: rec.Summary}},
						}, query); ok && len(candidates) < searchCandidateCap {
							candidates = append(candidates, candidate{syn,
								SearchHit{Title: title, Snippet: snippet}})
						}
						alive = append([]int{syn}, alive[len(alive)-kept:]...)
					}
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	survive := make(map[int]bool, len(alive))
	for _, ord := range alive {
		survive[ord] = true
	}
	var out []SearchHit
	for _, c := range candidates { // 文件顺序即命中顺序
		if survive[c.ordinal] {
			out = append(out, c.hit)
			if len(out) >= searchHitPerSess {
				break
			}
		}
	}
	return out
}

// matchSnippet reports whether any text in the message matches, with a
// window of context around the first hit.
func matchSnippet(m ai.Message, query string) (string, bool) {
	hay := strings.ToLower(m.Text())
	i := strings.Index(hay, query)
	if i < 0 {
		// 工具参数也参与（同会话内搜索的口径）：正在改的文件、正在跑的
		// 命令只在 args 里。
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockToolCall {
				if j := strings.Index(strings.ToLower(b.Arguments), query); j >= 0 {
					hay, i = strings.ToLower(b.Arguments), j
					break
				}
			}
		}
		if i < 0 {
			return "", false
		}
	}
	lo := i - searchSnippetWidth
	if lo < 0 {
		lo = 0
	}
	hi := i + len(query) + searchSnippetWidth
	if hi > len(hay) {
		hi = len(hay)
	}
	snippet := strings.TrimSpace(hay[lo:hi])
	if lo > 0 {
		snippet = "…" + snippet
	}
	if hi < len(hay) {
		snippet += "…"
	}
	return snippet, true
}

// dir returns the sessions directory（manager 的落盘处）.
func (s *AgentService) dir() string { return filepath.Dir(s.path) }
