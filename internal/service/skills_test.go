package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/skills"
)

// plantSkill 在工作区写一个技能目录。
func plantSkill(t *testing.T, ws, name, description string) {
	t.Helper()
	dir := filepath.Join(ws, ".zgo", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: " + description + "\n---\n技能正文"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSkillsAccessorDiscoversWorkspaceLayer 验证 /skills 的服务面发现工
// 作区层技能（本地路径形态）。
func TestSkillsAccessorDiscoversWorkspaceLayer(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	plantSkill(t, ws, "deploy", "Ship the service")
	plantSkill(t, ws, "broken", "ok") // 占位：再写一个坏技能验证诊断不吞
	bad := filepath.Join(ws, ".zgo", "skills", "broken", "SKILL.md")
	if err := os.WriteFile(bad, []byte("---\ndescription: no name\n---\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := svc.Skills(ws)
	found := false
	for _, s := range out.Skills {
		if s.Name == "deploy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("deploy not discovered: %+v", out.Skills)
	}
	if len(out.Diagnostics) == 0 {
		t.Fatal("broken skill produced no diagnostic")
	}
}

// TestSkillRootsShadowing 验证根顺序：工作区在前，同名覆盖（与自定义命
// 令同约定）——全局层的同名技能不遮蔽工作区层。
func TestSkillRootsShadowing(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	plantSkill(t, ws, "deploy", "工作区版")

	roots := svc.skillRoots("sess-shadow", ws)
	if len(roots) < 2 || roots[0].Scope != skills.ScopeProject || roots[1].Scope != skills.ScopeUser {
		t.Fatalf("roots order wrong: %+v", roots)
	}
	// 用假的全局根替换真 home（机器噪声隔离），同名技能落在第二根。
	roots[1].Base = filepath.Join(t.TempDir(), "global-skills")
	if err := os.MkdirAll(filepath.Join(roots[1].Base, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots[1].Base, "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: 全局版\n---\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := skills.Discover(roots)
	if len(out.Skills) != 1 || out.Skills[0].Description != "工作区版" {
		t.Fatalf("workspace layer should shadow global: %+v", out.Skills)
	}
}
