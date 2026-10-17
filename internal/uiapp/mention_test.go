package uiapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestMentionHintsView: typing a trailing @token floats matching
// workspace files; picking one replaces the token with the full path.
func TestMentionHintsView(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "noop.txt"), []byte("x"), 0o644); err != nil {
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

	a.input = "看看 @not"
	tt.Frame()
	if !tt.HasText("@notes.md") {
		t.Fatal("hint not shown")
	}

	// 点选补全：尾部 token 替换为完整路径。
	if err := tt.Click("@notes.md"); err != nil {
		t.Fatalf("hint not clickable: %v", err)
	}
	if a.input != "看看 notes.md " {
		t.Fatalf("input after pick = %q", a.input)
	}

	// 无匹配时不出浮层。
	a.input = "看看 @zzz"
	tt.Frame()
	if tt.HasText("@notes.md") {
		t.Fatal("hint shown without match")
	}
}

func TestTrailingMention(t *testing.T) {
	cases := map[string]string{
		"看看 @not":    "@not",
		"@not":       "@not",
		"a @x @y":    "@y",
		"no mention": "",
		"@":          "",
		"mail@x":     "", // @ 前不是分隔符：不算引用
	}
	for in, want := range cases {
		if got := trailingMention(in); got != want {
			t.Fatalf("trailingMention(%q) = %q, want %q", in, got, want)
		}
	}
}
