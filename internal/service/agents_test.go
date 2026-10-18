package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agents"
)

// plantAgent 在工作区写一个子代理定义文件。
func plantAgent(t *testing.T, ws, file, description, body string) {
	t.Helper()
	dir := filepath.Join(ws, ".zgo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ndescription: " + description + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, file), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAgentRootsLayering 验证子代理定义根顺序与遮蔽（spec 规则 51）：
// 工作区在前，同名覆盖全局——与技能同约定。
func TestAgentRootsLayering(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	plantAgent(t, ws, "reviewer.md", "工作区版", "P.")

	roots := svc.agentRoots("sess-agents", ws)
	if len(roots) < 2 || roots[0].Scope != agents.ScopeProject || roots[1].Scope != agents.ScopeUser {
		t.Fatalf("roots order wrong: %+v", roots)
	}
	// 假全局根替换真 home（机器噪声隔离），同名定义落在第二根。
	roots[1].Base = filepath.Join(t.TempDir(), "global-agents")
	if err := os.MkdirAll(roots[1].Base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots[1].Base, "other.md"),
		[]byte("---\nname: reviewer\ndescription: 全局版\n---\nG."), 0o644); err != nil {
		t.Fatal(err)
	}
	out := agents.Discover(roots)
	if len(out.Defs) != 1 {
		t.Fatalf("defs: %+v", out.Defs)
	}
	if out.Defs[0].Description != "工作区版" || out.Defs[0].Scope != agents.ScopeProject {
		t.Fatalf("workspace layer should shadow global: %+v", out.Defs)
	}
}

// TestFilterToolsByNameVocab 验证白名单过滤只收窄：词表外（task/bash）
// 即便列入白名单也不放行。
func TestFilterToolsByNameVocab(t *testing.T) {
	pool := []agent.Tool{fakeTool("read"), fakeTool("edit"),
		fakeTool("task"), fakeTool("bash")}
	got := filterToolsByName(pool, []string{"read", "edit", "task", "bash", "ghost"})
	if len(got) != 2 || got[0].Name() != "read" || got[1].Name() != "edit" {
		t.Fatalf("filter: %v", toolNames(got))
	}
}

// fakeTool 是按名自述的最小工具实现。
type fakeTool string

func (f fakeTool) Name() string               { return string(f) }
func (f fakeTool) Description() string        { return "" }
func (f fakeTool) Parameters() map[string]any { return nil }
func (f fakeTool) Execute(context.Context, json.RawMessage) agent.ToolResult {
	return agent.ToolResult{}
}

func toolNames(ts []agent.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}
