package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samplePatch builds a patch touching an existing file (update), a new
// file (add) and a doomed file (delete).
func samplePatch(t *testing.T, ws string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, "app.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "gone.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return "*** Begin Patch\n" +
		"*** Update File: app.txt\n" +
		"@@\n two\n-three\n+THREE\n" +
		"*** Add File: new.txt\n" +
		"+fresh line\n" +
		"*** Delete File: gone.txt\n" +
		"*** End Patch\n"
}

func patchArgs(patch string) []byte {
	return []byte(`{"patch":` + quote(patch) + `}`)
}

func quote(s string) string {
	// 简易 JSON 字符串编码：测试用，足够。
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestPatchPreviewDiffNoWrite: the preview covers update/add/delete in
// one aggregate and leaves the workspace untouched.
func TestPatchPreviewDiffNoWrite(t *testing.T) {
	ws := t.TempDir()
	patch := samplePatch(t, ws)
	p := NewPatch(Workspace{Root: ws})

	diff := p.PreviewDiff(patchArgs(patch))
	for _, want := range []string{"--- app.txt", "-three", "+THREE", "--- new.txt", "+fresh line", "--- gone.txt", "-bye"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff misses %q:\n%s", want, diff)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "app.txt")); string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("preview updated app.txt: %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("preview created new.txt")
	}
	if _, err := os.Stat(filepath.Join(ws, "gone.txt")); err != nil {
		t.Fatal("preview deleted gone.txt")
	}
}

// TestPatchExecuteCarriesDiff: execution reports the same aggregate on
// the result's Diff field.
func TestPatchExecuteCarriesDiff(t *testing.T) {
	ws := t.TempDir()
	patch := samplePatch(t, ws)
	p := NewPatch(Workspace{Root: ws})

	res := p.Execute(nil, patchArgs(patch))
	if res.IsError {
		t.Fatalf("execute failed: %s", res.Content)
	}
	for _, want := range []string{"--- app.txt", "+THREE", "--- new.txt", "-bye"} {
		if !strings.Contains(res.Diff, want) {
			t.Fatalf("result diff misses %q:\n%s", want, res.Diff)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "app.txt")); !strings.Contains(string(got), "THREE") {
		t.Fatalf("app.txt not updated: %q", got)
	}
}
