package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIgnoreListMatch 验证 git 风格模式子集的判定（issue #208）：目录限
// 定、锚定、反选、通配与静态兜底的并集语义。
func TestIgnoreListMatch(t *testing.T) {
	ig := &ignoreList{}
	for _, line := range []string{
		"vendor/",
		"*.log",
		"/rootonly.txt",
		"docs/build",
		"!keep.log",
	} {
		_ = line
	}
	// 直接构造（与 loadIgnores 的解析同路径经 TestLoadIgnoresParses 覆盖）。
	ig = parseLines(t, "vendor/\n*.log\n/rootonly.txt\ndocs/build\n!keep.log\n")

	cases := []struct {
		rel    string
		isDir  bool
		ignore bool
	}{
		{"vendor", true, true},           // 目录限定命中
		{"vendor", false, false},         // 文件名 vendor 不受目录限定
		{"a/vendor", true, true},         // 非锚定目录名任意层命中
		{"x/y.log", false, true},         // 通配命中
		{"keep.log", false, false},       // 反选后命中者胜
		{"sub/keep.log", false, false},   // 反选任意层
		{"rootonly.txt", false, true},    // 锚定命中
		{"a/rootonly.txt", false, false}, // 锚定不匹配子层
		{"docs/build", true, true},       // 带斜杠按根相对
		{"src/docs/build", true, false},  // 带斜杠不匹配任意层
		{"main.go", false, false},
	}
	for _, c := range cases {
		if got := ig.match(c.rel, c.isDir); got != c.ignore {
			t.Errorf("match(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.ignore)
		}
	}
}

// parseLines 经 loadIgnores 的解析路径构造（规则解析本身也被覆盖）。
func parseLines(t *testing.T, content string) *ignoreList {
	t.Helper()
	w := testWorkspace(t)
	if err := os.WriteFile(filepath.Join(w.Root, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return loadIgnores(w)
}

// TestLoadIgnoresMissing: 无 .gitignore → 空表（静态 skipDirs 兜底路径）。
func TestLoadIgnoresMissing(t *testing.T) {
	w := testWorkspace(t)
	if ig := loadIgnores(w); !ig.empty() {
		t.Fatalf("missing .gitignore should be empty: %+v", ig.patterns)
	}
}

// TestGrepHonorsGitignore: 集成——被忽略目录与 *.log 不进 grep 结果，
// 静态 skipDirs 依旧生效。
func TestGrepHonorsGitignore(t *testing.T) {
	w := testWorkspace(t)
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(w.Root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, c string) {
		mkdir(filepath.Dir(p))
		if err := os.WriteFile(filepath.Join(w.Root, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(w.Root, ".gitignore"), []byte("vendor/\n*.log\n"), 0o644)
	write("real/needle.txt", "needle here\n")
	write("vendor/needle.txt", "needle ignored\n")
	write("noise.log", "needle in log\n")
	write("node_modules/x.txt", "needle in deps\n") // 静态表

	g := NewGrep(w)
	res := execTool(t, g, `{"pattern":"needle"}`)
	if res.IsError {
		t.Fatalf("grep: %s", res.Content)
	}
	if want := "real/needle.txt"; !strings.Contains(res.Content, want) {
		t.Fatalf("real file missing from results:\n%s", res.Content)
	}
	for _, noise := range []string{"vendor/needle.txt", "noise.log", "node_modules"} {
		if strings.Contains(res.Content, noise) {
			t.Fatalf("ignored %q leaked into results:\n%s", noise, res.Content)
		}
	}
}
