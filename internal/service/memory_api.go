package service

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// MemoryEntry is one workspace's long-term memory summary（zgo memory list）.
type MemoryEntry struct {
	Workspace string
	Bytes     int64
	Modified  string // 空 = 尚无记忆
}

// MemoryOverview lists the memory state of every workspace the sessions
// reference（记忆按工作区分文件，会话是工作区的可见索引）.
func (s *AgentService) MemoryOverview() []MemoryEntry {
	seen := map[string]bool{}
	var out []MemoryEntry
	for _, info := range s.ListSessions() {
		ws := info.Workspace
		if ws == "" || seen[ws] {
			continue
		}
		seen[ws] = true
		entry := MemoryEntry{Workspace: ws}
		if st, err := os.Stat(tools.MemoryPath(s.memRoot, ws)); err == nil {
			entry.Bytes = st.Size()
			entry.Modified = st.ModTime().Local().Format("2006-01-02 15:04")
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Modified != out[j].Modified {
			return out[i].Modified > out[j].Modified // 最近写的在前；无记忆垫底
		}
		return out[i].Workspace < out[j].Workspace
	})
	return out
}

// ReadMemory returns a workspace's memory content ("" when none).
func (s *AgentService) ReadMemory(workspace string) string {
	return s.memoryFor(workspace).Read()
}

// AppendMemory adds a timestamped entry to a workspace's memory.
func (s *AgentService) AppendMemory(workspace, entry string) error {
	return s.memoryFor(workspace).Append(entry)
}

// ClearMemory removes a workspace's memory file (idempotent).
func (s *AgentService) ClearMemory(workspace string) error {
	if err := os.Remove(tools.MemoryPath(s.memRoot, workspace)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MemorySearchHit 是一条检索命中（spec 规则 119）。
type MemorySearchHit struct {
	Workspace string
	Line      string
}

// SearchMemory 跨工作区检索记忆（spec 规则 119）：子串匹配（大小写不
// 敏感），逐行扫描各工作区 memory 文件；命中行带上限（默认 5/工作区
// 20 总）防刷屏。空查询返回 nil。
func (s *AgentService) SearchMemory(query string) []MemorySearchHit {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	q := strings.ToLower(query)
	var out []MemorySearchHit
	for _, e := range s.MemoryOverview() {
		if e.Bytes == 0 {
			continue
		}
		data, err := os.ReadFile(tools.MemoryPath(
			filepath.Join(filepath.Dir(s.path), "memory"), e.Workspace))
		if err != nil {
			continue
		}
		hits := 0
		for _, line := range strings.Split(string(data), "\n") {
			if len(out) >= 20 {
				return out
			}
			if strings.Contains(strings.ToLower(line), q) {
				out = append(out, MemorySearchHit{Workspace: e.Workspace, Line: strings.TrimSpace(line)})
				hits++
				if hits >= 5 {
					break
				}
			}
		}
	}
	return out
}
