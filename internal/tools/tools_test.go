package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testWorkspace(t *testing.T) Workspace {
	t.Helper()
	w := Workspace{Root: t.TempDir()}
	return w
}

func run(t *testing.T, tool interface {
	Execute(context.Context, json.RawMessage) struct {
		Content string
		IsError bool
	}
}, args string) (string, bool) {
	t.Helper()
	res := tool.Execute(context.Background(), json.RawMessage(args))
	return res.Content, res.IsError
}

// mustRun adapts tools whose Execute returns agent.ToolResult.
func execTool[T any](t *testing.T, tool interface {
	Execute(context.Context, json.RawMessage) T
}, args string) T {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(args))
}

// TestWorkspaceJail verifies the path boundary both ways.
func TestWorkspaceJail(t *testing.T) {
	w := testWorkspace(t)
	if _, err := w.Resolve("sub/file.txt"); err != nil {
		t.Fatalf("relative path rejected: %v", err)
	}
	inside, _ := w.Resolve("file.txt")
	if got, err := w.Resolve(inside); err != nil || got != inside {
		t.Fatalf("absolute inside path: %q, %v", got, err)
	}
	if _, err := w.Resolve("../outside.txt"); err == nil {
		t.Fatal("parent escape accepted")
	}
	if _, err := w.Resolve(filepath.Join(w.Root, "..", "outside.txt")); err == nil {
		t.Fatal("absolute escape accepted")
	}
	if _, err := w.Resolve(""); err == nil {
		t.Fatal("empty path accepted")
	}
}

// TestReadNumbersLines verifies the line-numbered output the edit tool
// refers to.
func TestReadNumbersLines(t *testing.T) {
	w := testWorkspace(t)
	path := filepath.Join(w.Root, "a.txt")
	os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644)

	res := execTool(t, NewRead(w), `{"path":"a.txt"}`)
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "     1\talpha") || !strings.Contains(res.Content, "     2\tbeta") {
		t.Fatalf("output = %q", res.Content)
	}
}

// TestReadOffsetLimit verifies paging through large files.
func TestReadOffsetLimit(t *testing.T) {
	w := testWorkspace(t)
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString(strings.Repeat("x", 5) + "\n")
	}
	os.WriteFile(filepath.Join(w.Root, "big.txt"), []byte(b.String()), 0o644)

	res := execTool(t, NewRead(w), `{"path":"big.txt","offset":2,"limit":3}`)
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if got := strings.Count(res.Content, "xxxxx"); got != 3 {
		t.Fatalf("content lines = %d, want 3", got)
	}
	if !strings.Contains(res.Content, "… 6 more lines; continue with offset=5 …") {
		t.Fatalf("missing continuation hint: %q", res.Content)
	}
}

// TestWriteCreatesDirectories verifies write is wholesale.
func TestWriteCreatesDirectories(t *testing.T) {
	w := testWorkspace(t)
	res := execTool(t, NewWrite(w), `{"path":"d/e/f.txt","content":"hello"}`)
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	data, err := os.ReadFile(filepath.Join(w.Root, "d", "e", "f.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("file = %q, %v", data, err)
	}
}

// TestEditRequiresUniqueMatch verifies the exact-match discipline.
func TestEditRequiresUniqueMatch(t *testing.T) {
	w := testWorkspace(t)
	os.WriteFile(filepath.Join(w.Root, "a.txt"), []byte("one\ntwo\none\n"), 0o644)

	res := execTool(t, NewEdit(w), `{"path":"a.txt","old_string":"one","new_string":"1"}`)
	if !res.IsError || !strings.Contains(res.Content, "2 times") {
		t.Fatalf("ambiguous edit allowed: %+v", res)
	}

	res = execTool(t, NewEdit(w), `{"path":"a.txt","old_string":"two","new_string":"2"}`)
	if res.IsError {
		t.Fatalf("unique edit failed: %s", res.Content)
	}
	data, _ := os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "one\n2\none\n" {
		t.Fatalf("file = %q", data)
	}

	res = execTool(t, NewEdit(w), `{"path":"a.txt","old_string":"absent","new_string":"x"}`)
	if !res.IsError {
		t.Fatal("missing match allowed")
	}
}

// TestBashRuns verifies the platform shell and output capture.
func TestBashRuns(t *testing.T) {
	w := testWorkspace(t)
	command := "echo hello"
	if runtime.GOOS == "windows" {
		command = "Write-Output hello"
	}
	res := execTool(t, NewBash(w, NewJobs()), `{"command":"`+command+`"}`)
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Fatalf("output = %q", res.Content)
	}
}

// TestBashReportsExitCode keeps failures informative, not fatal.
func TestBashReportsExitCode(t *testing.T) {
	w := testWorkspace(t)
	command := "exit 3"
	if runtime.GOOS == "windows" {
		command = "exit 3"
	}
	res := execTool(t, NewBash(w, NewJobs()), `{"command":"`+command+`"}`)
	if !res.IsError {
		t.Fatal("failing command reported success")
	}
	if !strings.Contains(res.Content, "3") {
		t.Fatalf("output = %q", res.Content)
	}
}

// TestGrepFindsMatches verifies content search with context paths.
func TestGrepFindsMatches(t *testing.T) {
	w := testWorkspace(t)
	os.MkdirAll(filepath.Join(w.Root, "src", "sub"), 0o755)
	os.WriteFile(filepath.Join(w.Root, "src", "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	os.WriteFile(filepath.Join(w.Root, "src", "sub", "b.go"), []byte("package b\nfunc Bar() {}\n"), 0o644)
	os.WriteFile(filepath.Join(w.Root, ".git", "config"), []byte("func Skipped() {}\n"), 0o755)

	res := execTool(t, NewGrep(w), `{"pattern":"func","path":"src"}`)
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "src/a.go:2:func Foo() {}") {
		t.Fatalf("output = %q", res.Content)
	}
	if !strings.Contains(res.Content, "src/sub/b.go:2:func Bar() {}") {
		t.Fatalf("nested match missing: %q", res.Content)
	}
	if strings.Contains(res.Content, ".git") {
		t.Fatalf("skipped directory searched: %q", res.Content)
	}

	res = execTool(t, NewGrep(w), `{"pattern":"func","include":"a.go"}`)
	if strings.Contains(res.Content, "b.go") {
		t.Fatalf("include filter ignored: %q", res.Content)
	}
}

// TestGlobMatchesDoubleStar verifies ** depth handling.
func TestGlobMatchesDoubleStar(t *testing.T) {
	w := testWorkspace(t)
	os.MkdirAll(filepath.Join(w.Root, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(w.Root, "a", "x.go"), nil, 0o644)
	os.WriteFile(filepath.Join(w.Root, "a", "b", "y.go"), nil, 0o644)
	os.WriteFile(filepath.Join(w.Root, "top.txt"), nil, 0o644)

	res := execTool(t, NewGlob(w), `{"pattern":"**/*.go"}`)
	if res.IsError {
		t.Fatalf("glob failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a/x.go") || !strings.Contains(res.Content, "a/b/y.go") {
		t.Fatalf("output = %q", res.Content)
	}
	if strings.Contains(res.Content, "top.txt") {
		t.Fatalf("unmatched file returned: %q", res.Content)
	}

	res = execTool(t, NewGlob(w), `{"pattern":"*.txt"}`)
	if !strings.Contains(res.Content, "top.txt") {
		t.Fatalf("root-level glob = %q", res.Content)
	}
}

// TestLsListsSorted verifies directories first and the entry format.
func TestLsListsSorted(t *testing.T) {
	w := testWorkspace(t)
	os.WriteFile(filepath.Join(w.Root, "z.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(w.Root, "adir"), 0o755)

	res := execTool(t, NewLs(w), `{}`)
	if res.IsError {
		t.Fatalf("ls failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "adir/") {
		t.Fatalf("directory entry = %q", res.Content)
	}
	if !strings.Contains(res.Content, "z.txt") {
		t.Fatalf("file entry = %q", res.Content)
	}
	if strings.Index(res.Content, "adir") > strings.Index(res.Content, "z.txt") {
		t.Fatalf("directories not first: %q", res.Content)
	}
}

// TestToolArgumentValidation verifies malformed arguments are tool errors.
func TestToolArgumentValidation(t *testing.T) {
	w := testWorkspace(t)
	res := execTool(t, NewRead(w), `{not json`)
	if !res.IsError {
		t.Fatal("malformed JSON accepted")
	}
	res = execTool(t, NewRead(w), `{"path":123}`)
	if !res.IsError {
		t.Fatal("wrong argument type accepted")
	}
}
