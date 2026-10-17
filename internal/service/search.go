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
	searchHitCap       = 30 // 总命中上限
	searchHitPerSess   = 3  // 每会话命中上限
	searchSnippetWidth = 80 // 命中前后各取的字符数
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
		if len(hits) >= searchHitCap {
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
// matching; results keep the session title for display.
func scanSession(path, query, title string) []SearchHit {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []SearchHit
	r := bufio.NewReaderSize(f, 256*1024)
	for len(out) < searchHitPerSess {
		raw, rerr := r.ReadString('\n')
		if line := strings.TrimSpace(raw); line != "" && len(line) <= 16<<20 {
			var rec struct {
				Type    string     `json:"type"`
				Message ai.Message `json:"message"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == "message" {
				if snippet, ok := matchSnippet(rec.Message, query); ok {
					out = append(out, SearchHit{Title: title, Snippet: snippet})
				}
			}
		}
		if rerr != nil {
			break
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
