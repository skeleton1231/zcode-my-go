package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
)

// skillToolFixture 搭一个带技能的工具与根。
func skillToolFixture(t *testing.T) (agent.Tool, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".zgo", "skills", "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: deploy\ndescription: Ship the service\n---\n\n1. 跑测试\n2. 打镜像"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := func() []skills.Root {
		return []skills.Root{{Base: filepath.Join(ws, ".zgo", "skills"),
			Scope: skills.ScopeProject, FS: skills.LocalFS{}}}
	}
	return NewSkillTool(roots), ws
}

func TestSkillToolLoadsContent(t *testing.T) {
	tool, ws := skillToolFixture(t)
	res := tool.Execute(context.Background(),
		[]byte(`{"skill":"deploy","args":"v2"}`))
	if res.IsError {
		t.Fatalf("execute failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "1. 跑测试") || !strings.Contains(res.Content, "Arguments:\nv2") {
		t.Fatalf("content missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, "skill base directory: ") {
		t.Fatalf("base directory hint missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, filepath.ToSlash(filepath.Join(ws, ".zgo", "skills", "deploy"))) {
		t.Fatalf("base should be readable path: %q", res.Content)
	}
}

func TestSkillToolNotFoundListsAvailable(t *testing.T) {
	tool, _ := skillToolFixture(t)
	res := tool.Execute(context.Background(), []byte(`{"skill":"nope"}`))
	if !res.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(res.Content, "not found") || !strings.Contains(res.Content, "- deploy") {
		t.Fatalf("self-correction hint missing: %q", res.Content)
	}
}

func TestSkillToolEmptyName(t *testing.T) {
	tool, _ := skillToolFixture(t)
	res := tool.Execute(context.Background(), []byte(`{}`))
	if !res.IsError {
		t.Fatal("empty name must error")
	}
}
