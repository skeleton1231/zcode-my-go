package service

import (
	"os"
	"sort"

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
