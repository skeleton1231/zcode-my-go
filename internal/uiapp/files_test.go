package uiapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestFilesPanelTreeAndPreview: the panel lists the workspace tree, a
// directory expands on click, and clicking a file opens the read-only
// preview with its content.
func TestFilesPanelTreeAndPreview(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "readme.md"), []byte("# 你好"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "pkg", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(info.ID)
	tt.Frame()

	if a.filesOpen {
		t.Fatal("panel open without toggle")
	}
	a.ToggleFiles()
	tt.Frame()
	if !tt.HasText("文件") || !tt.HasText("readme.md") || !tt.HasText("pkg") {
		t.Fatal("tree not rendered")
	}

	// 展开目录（展开状态驱动渲染；箭头点选是 mygo TreeItem 自身的行为，
	// 由其测试覆盖）。
	a.treeExpanded["pkg"] = true
	tt.Frame()
	if !tt.HasText("main.go") {
		t.Fatal("nested file not listed")
	}

	// 点文件开预览。
	if err := tt.Click("readme.md"); err != nil {
		t.Fatalf("preview: %v", err)
	}
	tt.Frame()
	if !a.previewOpen || a.previewText != "# 你好" {
		t.Fatalf("preview = %q", a.previewText)
	}
	if !tt.HasText("# 你好") {
		t.Fatal("preview content not shown")
	}

	// 切会话清空树缓存。
	info2, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.OpenSession(info2.ID)
	if len(a.treeEntries) != 0 {
		t.Fatal("tree cache leaked across sessions")
	}
}
