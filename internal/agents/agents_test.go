package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memFS 是内存根（远端 SSH 适配同构面的最小替身）。
type memFS struct {
	files map[string]string
	join  func(parts ...string) string
}

func (m memFS) Join(parts ...string) string  { return m.join(parts...) }
func (m memFS) ReadFile(n string) ([]byte, error) {
	if c, ok := m.files[n]; ok {
		return []byte(c), nil
	}
	return nil, os.ErrNotExist
}
func (m memFS) ReadDir(dir string) ([]DirEntry, error) {
	seen := map[string]bool{}
	var out []DirEntry
	for name := range m.files {
		d, f := filepath.Split(name)
		if m.join([]string{d}...) == dir || (d == "" && dir == "") {
			if !seen[f] {
				seen[f] = true
				out = append(out, DirEntry{Name: f, IsDir: false})
			}
		}
	}
	if len(out) == 0 && len(m.files) == 0 {
		return nil, os.ErrNotExist
	}
	return out, nil
}

func local(t *testing.T, files map[string]string) Root {
	t.Helper()
	base := t.TempDir()
	for name, content := range files {
		p := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Root{Base: base, Scope: ScopeProject, FS: LocalFS{}}
}

func TestDiscoverParsesFrontmatterAndBody(t *testing.T) {
	root := local(t, map[string]string{
		"reviewer.md": "---\nname: reviewer\ndescription: Reviews code\ntools: read, grep\n---\nYou are a code reviewer.\n",
	})
	out := Discover([]Root{root})
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Defs) != 1 {
		t.Fatalf("defs: %+v", out.Defs)
	}
	d := out.Defs[0]
	if d.Name != "reviewer" || d.Description != "Reviews code" {
		t.Fatalf("meta: %+v", d)
	}
	if strings.Join(d.Tools, ",") != "read,grep" {
		t.Fatalf("tools: %v", d.Tools)
	}
	if d.System != "You are a code reviewer." {
		t.Fatalf("system: %q", d.System)
	}
}

func TestDiscoverNameDefaultsToFileStem(t *testing.T) {
	root := local(t, map[string]string{
		"linter.md": "---\ndescription: Finds lint issues\n---\nLint carefully.\n",
	})
	out := Discover([]Root{root})
	if len(out.Defs) != 1 || out.Defs[0].Name != "linter" {
		t.Fatalf("defs: %+v", out.Defs)
	}
}

func TestDiscoverRequiresDescriptionAndBody(t *testing.T) {
	root := local(t, map[string]string{
		"no-desc.md": "---\nname: x\n---\nBody.\n",
		"no-body.md": "---\nname: y\ndescription: d\n---\n\n",
	})
	out := Discover([]Root{root})
	if len(out.Defs) != 0 {
		t.Fatalf("defs: %+v", out.Defs)
	}
	if len(out.Diagnostics) != 2 {
		t.Fatalf("diagnostics: %+v", out.Diagnostics)
	}
}

func TestDiscoverProjectOverridesUserOnDupName(t *testing.T) {
	proj := local(t, map[string]string{
		"a.md": "---\nname: dup\ndescription: project\n---\nP.\n",
	})
	user := local(t, map[string]string{
		"b.md": "---\nname: dup\ndescription: user\n---\nU.\n",
	})
	user.Scope = ScopeUser
	out := Discover([]Root{proj, user})
	if len(out.Defs) != 1 || out.Defs[0].Description != "project" {
		t.Fatalf("priority broken: %+v", out.Defs)
	}
	if out.Defs[0].Scope != ScopeProject {
		t.Fatalf("scope: %+v", out.Defs[0])
	}
}

func TestDiscoverSkipsNonMarkdownAndDirs(t *testing.T) {
	root := local(t, map[string]string{
		"ok.md":    "---\ndescription: d\n---\nBody.\n",
		"notmd.txt": "ignore me",
	})
	out := Discover([]Root{root})
	if len(out.Defs) != 1 || out.Defs[0].Name != "ok" {
		t.Fatalf("defs: %+v", out.Defs)
	}
}

func TestDiscoverTruncatesOversizedPersona(t *testing.T) {
	root := local(t, map[string]string{
		"big.md": "---\ndescription: d\n---\n" + strings.Repeat("x", DefaultMaxBytes+50),
	})
	out := Discover([]Root{root})
	if len(out.Defs) != 1 {
		t.Fatalf("defs: %+v", out.Defs)
	}
	if !out.Defs[0].Truncated {
		t.Fatal("truncation not flagged")
	}
	if len(out.Defs[0].System) > DefaultMaxBytes+100 {
		t.Fatalf("system too large: %d", len(out.Defs[0].System))
	}
}

func TestToolsWhitelistParsesAndDedupes(t *testing.T) {
	root := local(t, map[string]string{
		"w.md": "---\nname: w\ndescription: d\ntools: read, read , grep,, ls\n---\nB.\n",
	})
	out := Discover([]Root{root})
	if strings.Join(out.Defs[0].Tools, ",") != "read,grep,ls" {
		t.Fatalf("tools: %v", out.Defs[0].Tools)
	}
}

func TestFindAndNames(t *testing.T) {
	root := local(t, map[string]string{
		"alpha.md": "---\ndescription: a\n---\nA.\n",
		"beta.md":  "---\ndescription: b\n---\nB.\n",
	})
	out := Discover([]Root{root})
	if got := strings.Join(Names(out), ","); got != "alpha,beta" {
		t.Fatalf("names: %v", got)
	}
	if _, ok := Find(out, "gamma"); ok {
		t.Fatal("gamma should miss")
	}
	if d, ok := Find(out, "beta"); !ok || d.Name != "beta" {
		t.Fatalf("find beta: %+v %v", d, ok)
	}
}

func TestSectionRosterAndBudget(t *testing.T) {
	root := local(t, map[string]string{
		"rev.md": "---\ndescription: Reviews code carefully\n---\nB.\n",
	})
	sec := Section(Discover([]Root{root}), 0)
	if !strings.Contains(sec, "- rev: Reviews code carefully") {
		t.Fatalf("section: %q", sec)
	}
	if !strings.Contains(sec, "task") {
		t.Fatalf("section should mention task: %q", sec)
	}
	tiny := Section(Discover([]Root{root}), 10)
	if strings.Contains(tiny, "Reviews") || !strings.Contains(tiny, "rev") {
		t.Fatalf("budget fallback: %q", tiny)
	}
	if Section(Outcome{}, 0) != "" {
		t.Fatal("empty outcome renders empty")
	}
}
