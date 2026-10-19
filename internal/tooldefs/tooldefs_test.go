package tooldefs

// 发现层测试：两层平铺/同名覆盖/frontmatter 校验/模板截断。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDef(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverTwoLayersAndShadowing(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	writeDef(t, ws, "deploy.md", "---\nname: deploy\ndescription: Ship it\n---\ngit push origin main")
	writeDef(t, home, "deploy.md", "---\nname: deploy\ndescription: 全局旧版\n---\necho global")
	writeDef(t, home, "lint.md", "---\nname: lint\ndescription: Lint the repo\n---\ngolangci-lint run")

	out := Discover([]Root{
		{Base: ws, Scope: ScopeProject, FS: LocalFS{}},
		{Base: home, Scope: ScopeUser, FS: LocalFS{}},
	})
	if len(out.Defs) != 2 {
		t.Fatalf("defs = %+v", out.Defs)
	}
	byName := map[string]Def{}
	for _, d := range out.Defs {
		byName[d.Name] = d
	}
	if byName["deploy"].Command != "git push origin main" || byName["deploy"].Scope != ScopeProject {
		t.Fatalf("workspace layer should shadow: %+v", byName["deploy"])
	}
	if byName["lint"].Scope != ScopeUser {
		t.Fatalf("global layer missing: %+v", byName["lint"])
	}
}

func TestDiscoverValidation(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "noname.md", "---\ndescription: no name\n---\necho x")
	writeDef(t, dir, "nodesc.md", "---\nname: nodesc\n---\necho x")
	writeDef(t, dir, "nobody.md", "---\nname: nobody\ndescription: no template\n---\n")
	writeDef(t, dir, "good.md", "---\nname: good\ndescription: fine\n---\necho ok")
	writeDef(t, dir, "ignored.txt", "not md")

	out := Discover([]Root{{Base: dir, Scope: ScopeProject, FS: LocalFS{}}})
	if len(out.Defs) != 1 || out.Defs[0].Name != "good" {
		t.Fatalf("defs = %+v (want only good)", out.Defs)
	}
}

func TestTemplateCaps(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "big.md", "---\nname: big\ndescription: "+strings.Repeat("d", 2000)+"\n---\n"+strings.Repeat("c", 20_000))
	out := Discover([]Root{{Base: dir, Scope: ScopeProject, FS: LocalFS{}}})
	if len(out.Defs) != 1 {
		t.Fatalf("defs = %+v", out.Defs)
	}
	d := out.Defs[0]
	if len(d.Description) > maxDescriptionChars {
		t.Fatalf("description not capped: %d", len(d.Description))
	}
	if len(d.Command) > maxTemplateBytes {
		t.Fatalf("template not capped: %d", len(d.Command))
	}
}

// TestMissingRootClean：缺目录零定义零诊断。
func TestMissingRootClean(t *testing.T) {
	out := Discover([]Root{{Base: filepath.Join(t.TempDir(), "nope"), Scope: ScopeUser, FS: LocalFS{}}})
	if len(out.Defs) != 0 || len(out.Diagnostics) != 0 {
		t.Fatalf("missing root noisy: %+v", out)
	}
}
