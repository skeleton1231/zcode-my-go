package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryRoundTrip(t *testing.T) {
	path := MemoryPath(t.TempDir(), `D:\ws\one`)
	if filepath.Base(path) == "ws_.md" {
		t.Fatalf("unhashed path: %s", path)
	}
	mem := NewMemory(path)
	if mem.Read() != "" || mem.Section() != "" {
		t.Fatal("empty memory not empty")
	}
	if err := mem.Append("用户偏好中文注释"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mem.Read(), "用户偏好中文注释") {
		t.Fatalf("read = %q", mem.Read())
	}
	if !strings.Contains(mem.Section(), "Long-term memory") {
		t.Fatalf("section = %q", mem.Section())
	}
	// 不同 workspace 不同文件。
	other := NewMemory(MemoryPath(t.TempDir(), `D:\ws\two`))
	if other.Read() != "" {
		t.Fatal("workspaces share memory")
	}
}

func TestMemoryToolOps(t *testing.T) {
	mem := NewMemory(MemoryPath(t.TempDir(), "ws"))
	tool := NewMemoryTool(mem)

	res := tool.Execute(context.Background(), json.RawMessage(`{"op":"read"}`))
	if res.IsError || res.Content != "(记忆为空)" {
		t.Fatalf("empty read = %+v", res)
	}
	res = tool.Execute(context.Background(), json.RawMessage(`{"op":"write","content":"项目用 pnpm"}`))
	if res.IsError || !strings.Contains(res.Content, "已记住") {
		t.Fatalf("write = %+v", res)
	}
	res = tool.Execute(context.Background(), json.RawMessage(`{"op":"read"}`))
	if !strings.Contains(res.Content, "项目用 pnpm") {
		t.Fatalf("read after write = %s", res.Content)
	}
	res = tool.Execute(context.Background(), json.RawMessage(`{"op":"delete"}`))
	if !res.IsError {
		t.Fatal("bad op accepted")
	}
}
