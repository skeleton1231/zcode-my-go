package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditPreviewDiffNoWrite: the preview shows the would-be change and
// leaves the file untouched.
func TestEditPreviewDiffNoWrite(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEdit(Workspace{Root: ws})

	diff := e.PreviewDiff([]byte(`{"path":"a.txt","old_string":"beta","new_string":"BETA"}`))
	if !strings.Contains(diff, "-beta") || !strings.Contains(diff, "+BETA") || !strings.Contains(diff, " alpha") {
		t.Fatalf("diff = %q", diff)
	}
	if got, _ := os.ReadFile(path); string(got) != "alpha\nbeta\n" {
		t.Fatalf("preview wrote the file: %q", got)
	}
	// 不匹配的 old_string：无 diff，不报错。
	if got := e.PreviewDiff([]byte(`{"path":"a.txt","old_string":"nope","new_string":"x"}`)); got != "" {
		t.Fatalf("mismatched preview = %q", got)
	}
}

// TestWritePreviewDiff: new files diff from empty; existing files show the
// replacement; nothing lands on disk.
func TestWritePreviewDiff(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "old.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewWrite(Workspace{Root: ws})

	fresh := w.PreviewDiff([]byte(`{"path":"new.txt","content":"after\n"}`))
	if !strings.Contains(fresh, "+after") || strings.Contains(fresh, "-before") {
		t.Fatalf("fresh diff = %q", fresh)
	}
	over := w.PreviewDiff([]byte(`{"path":"old.txt","content":"after\n"}`))
	if !strings.Contains(over, "-before") || !strings.Contains(over, "+after") {
		t.Fatalf("overwrite diff = %q", over)
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("preview wrote new.txt")
	}
}
