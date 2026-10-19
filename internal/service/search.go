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
	"github.com/skeleton1231/zcode-my-go/internal/sessionidx"
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
	// 索引保鲜（spec 规则 71）：(size, mtime) 命中的会话不重读文件；
	// 变更异步落盘（索引是加速器不是账本——写失败下次重建）。
	ids := make([]string, len(sessions))
	for i, info := range sessions {
		ids[i] = info.ID
	}
	if s.searchIdx.Refresh(ids, filepath.Join(s.dir(), "sessions")) {
		// 异步落盘须被 Close 收束（spec 规则 71）——goroutine 活过
		// TempDir 清理会复活文件（#160 同族教训）。
		s.idxWG.Add(1)
		go func() {
			defer s.idxWG.Done()
			_ = s.searchIdx.Save()
		}()
	}
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
			var found []SearchHit
			// 索引优先（O(1) 查段）；未命中（超限/损坏/缺失）退 live 扫描
			//——两条路径消费同一抽取语义，结果按构造等价。
			if e := s.searchIdx.Lookup(info.ID); e != nil {
				found = matchSegments(e.Segments, query, info.Title)
			} else {
				found = scanSession(filepath.Join(s.dir(), "sessions", info.ID+".jsonl"), query, info.Title)
			}
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
	return snippetAround(hay, i, len(query)), true

}

// snippetAround 截命中窗口（live 扫描与索引段共用——两路径片段逐字节
// 一致是等价断言的锚点）。
func snippetAround(hay string, i, qlen int) string {
	lo := i - searchSnippetWidth
	if lo < 0 {
		lo = 0
	}
	hi := i + qlen + searchSnippetWidth
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
	return snippet
}

// matchSegments 在索引段上复刻 scanSession 的命中语义：按段序（=文件
// 序）扫，消息的首个命中段定片段（matchSnippet 优先序——文本拼接段
// 在前、工具参数段在后），同消息其余段跳过；候选上限与每会话产出同
// 活路径（段已只含重放态存活消息，无 alive 过滤步骤）。
func matchSegments(segs []sessionidx.Segment, query, title string) []SearchHit {
	var out []SearchHit
	const none = -1 << 30
	lastOrd := none
	for n, seg := range segs {
		if seg.Ord == lastOrd {
			continue // 该消息已入候选（首段即定片段）
		}
		i := strings.Index(seg.Text, query)
		if i < 0 {
			continue
		}
		lastOrd = seg.Ord
		if n > searchCandidateCap {
			break
		}
		out = append(out, SearchHit{Title: title, Snippet: snippetAround(seg.Text, i, len(query))})
		if len(out) >= searchHitPerSess {
			break
		}
	}
	return out
}

// dir returns the sessions directory（manager 的落盘处）.
func (s *AgentService) dir() string { return filepath.Dir(s.path) }
