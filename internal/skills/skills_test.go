package skills

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localRoot 在临时目录里搭一个本地技能根。
func localRoot(t *testing.T, scope Scope) Root {
	t.Helper()
	return Root{Base: t.TempDir(), Scope: scope, FS: LocalFS{}}
}

// writeSkill 在根内写一个技能目录（dir 为空表示根级 SKILL.md）。
func writeSkill(t *testing.T, r Root, dir, content string) {
	t.Helper()
	rel := skillFile
	if dir != "" {
		rel = filepath.Join(dir, skillFile)
	}
	full := r.FS.Join(r.Base, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFindsSkillsSortedAndDeduped(t *testing.T) {
	r := localRoot(t, ScopeProject)
	writeSkill(t, r, "deploy", "---\nname: deploy\ndescription: Ship it\n---\n步骤一")
	writeSkill(t, r, "audit", "---\nname: audit\ndescription: 审计依赖\nwhen_to_use: 升级前\n---\n正文")
	// 非 SKILL.md 的文件与普通文件不收录。
	if err := os.WriteFile(r.FS.Join(r.Base, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := Discover([]Root{r})
	if len(out.Skills) != 2 || out.Skills[0].Name != "audit" || out.Skills[1].Name != "deploy" {
		t.Fatalf("unexpected skills: %+v", out.Skills)
	}
	if out.Skills[0].WhenToUse != "升级前" {
		t.Fatalf("when_to_use not parsed: %+v", out.Skills[0])
	}
	if !strings.Contains(out.Skills[0].Path, "audit/SKILL.md") {
		t.Fatalf("path not slash-formed: %s", out.Skills[0].Path)
	}
}

func TestDiscoverNoFrontmatterUsesDirName(t *testing.T) {
	r := localRoot(t, ScopeProject)
	writeSkill(t, r, "handwritten", "纯正文技能")
	out := Discover([]Root{r})
	if len(out.Skills) != 1 || out.Skills[0].Name != "handwritten" || out.Skills[0].Description != "" {
		t.Fatalf("handwritten skill not accepted: %+v", out.Skills)
	}
}

func TestDiscoverFrontmatterValidation(t *testing.T) {
	cases := []struct {
		name, content, wantDiag string
	}{
		{"no-name", "---\ndescription: d\n---\nx", "must include a name"},
		{"no-desc", "---\nname: a\n---\nx", "must include a description"},
		{"bad-name", "---\nname: ../evil\ndescription: d\n---\nx", "invalid skill name"},
		{"space-name", "---\nname: has space\ndescription: d\n---\nx", "invalid skill name"},
		{"long-desc", "---\nname: a\ndescription: " + strings.Repeat("x", maxDescriptionChars+1) + "\n---\nx", "too long"},
	}
	for _, c := range cases {
		r := localRoot(t, ScopeProject)
		writeSkill(t, r, c.name, c.content)
		out := Discover([]Root{r})
		if len(out.Skills) != 0 {
			t.Fatalf("%s: skill accepted: %+v", c.name, out.Skills)
		}
		found := false
		for _, d := range out.Diagnostics {
			if strings.Contains(d.Message, c.wantDiag) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: diagnostic %q missing in %+v", c.name, c.wantDiag, out.Diagnostics)
		}
	}
}

func TestDiscoverRootLevelSkill(t *testing.T) {
	r := localRoot(t, ScopeProject)
	writeSkill(t, r, "", "---\nname: solo\ndescription: single-skill root\n---\n正文")
	out := Discover([]Root{r})
	if len(out.Skills) != 1 || out.Skills[0].Name != "solo" {
		t.Fatalf("root-level SKILL.md not found: %+v", out.Skills)
	}
}

func TestDiscoverFirstRootWinsOnDuplicateName(t *testing.T) {
	project := localRoot(t, ScopeProject)
	writeSkill(t, project, "deploy", "---\nname: deploy\ndescription: 项目版\n---\n项目步骤")
	user := localRoot(t, ScopeUser)
	writeSkill(t, user, "deploy", "---\nname: deploy\ndescription: 全局版\n---\n全局步骤")
	out := Discover([]Root{project, user})
	if len(out.Skills) != 1 || out.Skills[0].Description != "项目版" {
		t.Fatalf("duplicate not shadowed by first root: %+v", out.Skills)
	}
}

func TestDiscoverMissingRootIsEmpty(t *testing.T) {
	r := Root{Base: filepath.Join(t.TempDir(), "nope"), Scope: ScopeProject, FS: LocalFS{}}
	if out := Discover([]Root{r}); len(out.Skills) != 0 || len(out.Diagnostics) != 0 {
		t.Fatalf("missing root produced noise: %+v", out)
	}
}

// fakeFS 用内存 map 模拟远端根（路径斜杠形态），验证 FS 抽象对远端适配
// 成立：Discover 不关心根的物理位置。
type fakeFS struct {
	files map[string]string
	dirs  map[string][]DirEntry
}

func (f fakeFS) Join(parts ...string) string {
	joined := ""
	for i, p := range parts {
		if i > 0 {
			joined += "/"
		}
		joined += p
	}
	return joined
}

func (f fakeFS) ReadFile(name string) ([]byte, error) {
	if c, ok := f.files[name]; ok {
		return []byte(c), nil
	}
	return nil, fs.ErrNotExist
}

func (f fakeFS) ReadDir(dir string) ([]DirEntry, error) {
	if es, ok := f.dirs[dir]; ok {
		return es, nil
	}
	return nil, fs.ErrNotExist
}

func TestDiscoverOverRemoteShapedFS(t *testing.T) {
	r := Root{Base: ".zgo/skills", Scope: ScopeProject, FS: fakeFS{
		files: map[string]string{
			".zgo/skills/remote-pack/SKILL.md": "---\nname: remote-pack\ndescription: over the wire\n---\n远端正文",
		},
		dirs: map[string][]DirEntry{
			".zgo/skills": {{Name: "remote-pack", IsDir: true}},
		},
	}}
	out := Discover([]Root{r})
	if len(out.Skills) != 1 || out.Skills[0].Name != "remote-pack" {
		t.Fatalf("remote-shaped root not discovered: %+v", out.Skills)
	}
	if out.Skills[0].Path != ".zgo/skills/remote-pack/SKILL.md" {
		t.Fatalf("path not root-relative: %s", out.Skills[0].Path)
	}
}

func TestLoadSkillStripsFrontmatterAndTruncates(t *testing.T) {
	r := localRoot(t, ScopeProject)
	writeSkill(t, r, "deploy", "---\nname: deploy\ndescription: d\n---\n\n正文开头\n正文结尾\n")
	out := Discover([]Root{r})
	s, ok := Find(out, "deploy")
	if !ok {
		t.Fatal("not found")
	}
	content, base, truncated, err := LoadSkill(r, s, DefaultMaxSkillBytes)
	if err != nil || truncated {
		t.Fatalf("load: %v truncated=%v", err, truncated)
	}
	if content != "正文开头\n正文结尾" {
		t.Fatalf("frontmatter not stripped / not trimmed: %q", content)
	}
	if !strings.HasSuffix(base, "deploy") {
		t.Fatalf("base dir wrong: %s", base)
	}
	// 超限截断：maxBytes 卡在正文中间即标注截断。
	_, _, truncated, err = LoadSkill(r, s, 30)
	if err != nil || !truncated {
		t.Fatalf("truncation not flagged: err=%v truncated=%v", err, truncated)
	}
}

func TestFindMissing(t *testing.T) {
	out := Discover([]Root{})
	if _, ok := Find(out, "nope"); ok {
		t.Fatal("found in empty outcome")
	}
}

func TestSectionRendersAndDegradesToNames(t *testing.T) {
	r := localRoot(t, ScopeProject)
	writeSkill(t, r, "deploy", "---\nname: deploy\ndescription: Ship the service\nwhen_to_use: releasing\n---\nx")
	out := Discover([]Root{r})
	sec := Section(out, DefaultMetaBudget)
	if !strings.Contains(sec, "- deploy: Ship the service — releasing") ||
		!strings.Contains(sec, "(file: ") {
		t.Fatalf("full form wrong: %q", sec)
	}
	// 预算压到放不下完整行：降级为仅名。
	degraded := Section(out, len("The following skills are available (the skill tool loads one by name):"))
	if !strings.Contains(degraded, "- deploy (file: ") || strings.Contains(degraded, "Ship the service") {
		t.Fatalf("names-only fallback wrong: %q", degraded)
	}
	if Section(Outcome{}, DefaultMetaBudget) != "" {
		t.Fatal("empty outcome should render empty")
	}
}

func TestParseFlatYAMLQuotedAndComments(t *testing.T) {
	v := parseFlatYAML("# 注释\nname: \"quoted name\"\ndescription: 'single'\nempty:\nno-colon-line")
	if v["name"] != "quoted name" || v["description"] != "single" {
		t.Fatalf("quoted values wrong: %v", v)
	}
}

// TestReadErrorBecomesDiagnostic 验证非 ErrNotExist 的读失败记诊断而非静默。
func TestReadErrorBecomesDiagnostic(t *testing.T) {
	r := Root{Base: "x", Scope: ScopeProject, FS: brokenFS{}}
	out := Discover([]Root{r})
	if len(out.Diagnostics) == 0 {
		t.Fatal("read error silently swallowed")
	}
}

type brokenFS struct{}

func (brokenFS) Join(parts ...string) string { return strings.Join(parts, "/") }

func (brokenFS) ReadFile(string) ([]byte, error) { return nil, errors.New("EACCES") }

func (brokenFS) ReadDir(string) ([]DirEntry, error) { return nil, errors.New("EACCES") }
