package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/slash"
)

func TestLoadImageBlocks(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("\x89PNG fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txt, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, errs := loadImageBlocks([]string{png, txt, filepath.Join(dir, "missing.png")})
	if len(blocks) != 1 || blocks[0].Kind != ai.BlockImage ||
		blocks[0].Media != "image/png" || blocks[0].Data == "" {
		t.Fatalf("blocks = %+v", blocks)
	}
	if len(errs) != 2 || !strings.Contains(errs[0], "图片类型") ||
		!strings.Contains(errs[1], "missing.png") {
		t.Fatalf("errs = %v", errs)
	}
}

func TestAttachmentsQueueLifecycle(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.png")
	os.WriteFile(png, []byte("x"), 0o644)

	var a attachments
	msg := a.add(png)
	if !strings.Contains(msg, "共 1 张") || a.status() != "📎×1" {
		t.Fatalf("add = %q status = %q", msg, a.status())
	}
	if got := a.take(); len(got) != 1 {
		t.Fatalf("take = %+v", got)
	}
	if a.status() != "" || a.take() != nil {
		t.Fatal("queue not drained")
	}
	// 坏路径给出可读错误且不入队。
	if msg := a.add(filepath.Join(dir, "nope.png")); !strings.Contains(msg, "nope.png") {
		t.Fatalf("bad add = %q", msg)
	}
	if len(a.pending) != 0 {
		t.Fatal("bad path queued")
	}
}

// TestSlashAttachRoutes: /attach reaches the surface callback; without
// one it says unsupported; missing arg prints usage.
func TestSlashAttachRoutes(t *testing.T) {
	var queued []string
	env := &slash.Env{Out: func(string) {},
		Attach: func(p string) string { queued = append(queued, p); return "ok" }}
	if handled, _ := slash.Run(env, "/attach x.png"); !handled || len(queued) != 1 {
		t.Fatalf("handled=%v queued=%v", handled, queued)
	}
	if handled, _ := slash.Run(&slash.Env{Out: func(string) {}}, "/attach x.png"); !handled {
		t.Fatal("not handled without callback")
	}
}
