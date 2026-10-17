package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceTreeAndPreview: the tree lists dirs-first sorted; the
// preview reads capped text and refuses binaries.
func TestWorkspaceTreeAndPreview(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	mkdir := func(name string) {
		if err := os.MkdirAll(filepath.Join(ws, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("sub")
	write("z-note.txt", "hi")
	write("a-first.md", "# head")
	write("sub/inner.txt", "inner content")
	write("blob.bin", "\x00\x01binary")
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := svc.WorkspaceTree(info.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// 目录优先，其余按名排序。
	if len(entries) != 4 || !entries[0].IsDir || entries[0].Name != "sub" ||
		entries[1].Name != "a-first.md" || entries[2].Name != "blob.bin" ||
		entries[3].Name != "z-note.txt" {
		t.Fatalf("entries = %+v", entries)
	}
	inner, err := svc.WorkspaceTree(info.ID, "sub")
	if err != nil || len(inner) != 1 || inner[0].Name != "inner.txt" || inner[0].IsDir {
		t.Fatalf("inner = %+v, %v", inner, err)
	}

	got, err := svc.ReadWorkspaceFile(info.ID, "sub/inner.txt")
	if err != nil || got != "inner content" {
		t.Fatalf("preview = %q, %v", got, err)
	}
	if _, err := svc.ReadWorkspaceFile(info.ID, "blob.bin"); err == nil ||
		!strings.Contains(err.Error(), "二进制") {
		t.Fatalf("binary err = %v", err)
	}
	// 越界拒绝。
	if _, err := svc.ReadWorkspaceFile(info.ID, "../outside.txt"); err == nil {
		t.Fatal("jail escape")
	}
}
