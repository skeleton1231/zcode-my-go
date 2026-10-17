package slash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestRunExportDefaultsAndExplicit: /export writes an HTML file by
// default, /export md writes Markdown, an explicit path wins, and bad
// writes report rather than crash.
func TestRunExportDefaultsAndExplicit(t *testing.T) {
	env, out := newEnv(t)
	svc := env.Svc
	id := env.SessionID()
	if err := svc.AppendForTesting(id, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "导出内容测试"}}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	Run(env, "/export")
	files := listDir(t, dir)
	if len(files) != 1 || !strings.HasSuffix(files[0], ".html") {
		t.Fatalf("default export = %v (out=%v)", files, *out)
	}
	html := read(t, filepath.Join(dir, files[0]))
	if !strings.Contains(html, "导出内容测试") {
		t.Fatalf("html missing content: %.200s", html)
	}

	Run(env, "/export md")
	files = listDir(t, dir)
	if len(files) != 2 {
		t.Fatalf("md export missing: %v", files)
	}
	found := false
	for _, f := range files {
		if strings.HasSuffix(f, ".md") {
			found = true
			md := read(t, filepath.Join(dir, f))
			if !strings.Contains(md, "导出内容测试") {
				t.Fatalf("md missing content: %.200s", md)
			}
		}
	}
	if !found {
		t.Fatalf("no .md among %v", files)
	}

	// 显式路径（含 html 形参覆盖 md）。
	dest := filepath.Join(dir, "custom.html")
	Run(env, "/export md "+dest)
	if got := read(t, dest); !strings.Contains(got, "导出内容测试") {
		t.Fatalf("explicit export empty: %.100s", got)
	}

	// /help 收录。
	Run(env, "/help")
	if !strings.Contains(strings.Join(*out, "\n"), "/export") {
		t.Fatal("/help missing /export")
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
