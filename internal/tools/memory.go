package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// memoryCapBytes bounds what the system prompt injects from memory.
const memoryCapBytes = 4 << 10

// Memory is the workspace-scoped long-term store (ZCode's memory package):
// one Markdown file per workspace under the app's memory directory,
// surviving sessions. Writes are serialized with a mutex because the tool
// and the prompt read it concurrently.
type Memory struct {
	mu   sync.Mutex
	path string
}

// MemoryPath names a workspace's memory file inside memRoot.
// SHA-256 前 16 字节做文件名（issue #20）：31 进制哈希可数学构造碰撞，
// 碰撞的工作区会互相读到并污染对方记忆（"aa"/"bB" 一对就撞）。
func MemoryPath(memRoot, workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(memRoot, fmt.Sprintf("ws_%x.md", sum[:16]))
}

// NewMemory builds the store over a workspace memory file.
func NewMemory(path string) *Memory { return &Memory{path: path} }

// Read returns the memory file's content ("" when absent).
func (m *Memory) Read() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return ""
	}
	if len(data) > memoryCapBytes {
		data = data[len(data)-memoryCapBytes:] // 提示只带最近的部分
	}
	return string(data)
}

// Append adds a timestamped entry.
func (m *Memory) Append(entry string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(m.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n## %s\n%s\n", time.Now().Format("2006-01-02 15:04"), strings.TrimSpace(entry))
	return err
}

// Section renders the memory for system-prompt injection ("" when empty).
func (m *Memory) Section() string {
	content := m.Read()
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return "\nLong-term memory for this workspace (from earlier sessions):\n" + content + "\n"
}

// memoryTool exposes the store to the model.
type memoryTool struct{ mem *Memory }

// NewMemoryTool builds the memory tool.
func NewMemoryTool(mem *Memory) agent.Tool { return &memoryTool{mem: mem} }

// Name implements agent.Tool.
func (t *memoryTool) Name() string { return "memory" }

// Description implements agent.Tool.
func (t *memoryTool) Description() string {
	return "Long-term memory for this workspace, shared across sessions. " +
		"op=read shows it; op=write appends a durable note (preferences, " +
		"decisions, project conventions worth remembering). Keep entries short " +
		"and factual."
}

// Parameters implements agent.Tool.
func (t *memoryTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"op": map[string]any{
			"type":        "string",
			"description": "read the memory, or append to it",
			"enum":        []string{"read", "write"},
		},
		"content": prop("string", "op=write: the note to remember"),
	}, "op")
}

// Execute implements agent.Tool.
func (t *memoryTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Op      string `json:"op"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	switch a.Op {
	case "read":
		if content := t.mem.Read(); content == "" {
			return okResult("(记忆为空)")
		} else {
			return agent.ToolResult{Content: content}
		}
	case "write":
		if strings.TrimSpace(a.Content) == "" {
			return errResult("op=write needs content")
		}
		if err := t.mem.Append(a.Content); err != nil {
			return errResult("writing memory: %v", err)
		}
		return okResult("已记住：%s", firstLine(a.Content))
	default:
		return errResult("unknown op %q (read or write)", a.Op)
	}
}

// Capability implements agent.Capabler: memory lives outside the workspace
// and holds notes only.
func (t *memoryTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
