package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// mentionEnv boots a service with a session over a temp workspace and
// seeds files.
func mentionEnv(t *testing.T) (svc *AgentService, sessionID, ws string) {
	t.Helper()
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws = t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(ws, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.md", "# 笔记\n内容一行")
	write("img/pic.png", "\x89PNG fake-bytes")
	write("big.txt", strings.Repeat("x", mentionTextCap+100))
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	return svc, info.ID, ws
}

func TestExpandMentionsTextAndImages(t *testing.T) {
	svc, id, _ := mentionEnv(t)

	text, media := svc.expandMentions(id, "看看 @notes.md 与 @img/pic.png", nil)
	if !strings.Contains(text, "看看 @notes.md") || !strings.Contains(text, "@img/pic.png") {
		t.Fatalf("tokens not kept: %q", text)
	}
	if !strings.Contains(text, "# 笔记") {
		t.Fatalf("file content not appended: %q", text)
	}
	if !strings.Contains(text, "```") {
		t.Fatalf("not fenced: %q", text)
	}
	if len(media) != 1 || media[0].Kind != ai.BlockImage ||
		media[0].Media != "image/png" || media[0].Data == "" {
		t.Fatalf("media = %+v", media)
	}
}

func TestExpandMentionsMissingAndCapped(t *testing.T) {
	svc, id, _ := mentionEnv(t)

	// 不存在的引用原样保留，不报错。
	text, media := svc.expandMentions(id, "看看 @nope.txt", nil)
	if text != "看看 @nope.txt" || media != nil {
		t.Fatalf("text = %q, media = %v", text, media)
	}
	// 超大文本截断标注。
	text, _ = svc.expandMentions(id, "读 @big.txt", nil)
	if !strings.Contains(text, "（截断）") {
		t.Fatalf("oversized not capped: %d chars", len(text))
	}
	// 路径越界不展开。
	text, _ = svc.expandMentions(id, "试试 @../etc/passwd", nil)
	if strings.Contains(text, "root:") || strings.Contains(text, "```") {
		t.Fatalf("jail escaped: %q", text)
	}
}

func TestWorkspaceFilesHints(t *testing.T) {
	svc, id, _ := mentionEnv(t)
	hints := svc.WorkspaceFiles(id, "notes")
	if len(hints) != 1 || hints[0] != "notes.md" {
		t.Fatalf("hints = %v", hints)
	}
	hints = svc.WorkspaceFiles(id, "zzz")
	if len(hints) != 0 {
		t.Fatalf("hints = %v", hints)
	}
}
