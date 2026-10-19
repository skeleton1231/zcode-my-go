package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	exporthtml "github.com/skeleton1231/zcode-my-go/internal/export"
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

// TestMentionTotalBudget 验证多文件引用的总量/数量预算（issue #120：50×
// 60KB 曾逼近 JSONL 行上限，重载整条消息静默消失——#105 的文本载荷版）。
func TestMentionTotalBudget(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	scrubEnv(t)
	ws := t.TempDir()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	// 35 个 60KB 文本文件（超 mentionMaxFiles=30，且总量近 2.1MB）。
	text := "都看看"
	for i := 0; i < 35; i++ {
		name := fmt.Sprintf("f%02d.log", i)
		if err := os.WriteFile(filepath.Join(ws, name),
			[]byte(strings.Repeat("数", 20<<10)), 0o644); err != nil {
			t.Fatal(err)
		}
		text += " @" + name
	}
	out, _ := svc.expandMentions(info.ID, text, nil)
	// 数量上限生效：30 个展开 + 注明 5 个未展开。
	if !strings.Contains(out, "未展开") {
		t.Fatal("budget notice missing")
	}
	if n := strings.Count(out, "：\n```"); n > 30 {
		t.Fatalf("expanded %d files, want <= 30", n)
	}
	// 总展开量受预算约束（2MB + 原文 + 围栏开销）。
	if len(out) > mentionTotalCap+64<<10 {
		t.Fatalf("expanded output = %d bytes", len(out))
	}
}

// TestMentionFenceSurvivesMarkdown: @ 引用的 Markdown 含行首裸 ```/````
// 时围栏必须加长（issue #178：#71 的动态围栏方案没带到 @ 展开与 stdin
// 管道两个新位点——固定三反引号被内容提前闭合，其后内容裸奔进提示）。
func TestMentionFenceSurvivesMarkdown(t *testing.T) {
	svc, id, ws := mentionEnv(t)
	md := "# 文档\n\n```go\nfmt.Println(1)\n```\n\n````md\n嵌套围栏\n````\n\n正文"
	if err := os.WriteFile(filepath.Join(ws, "doc.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := svc.expandMentions(id, "读 @doc.md", nil)
	// 内容完整在场（未被围栏碰撞截走）。
	for _, want := range []string{"fmt.Println(1)", "嵌套围栏", "正文"} {
		if !strings.Contains(out, want) {
			t.Fatalf("content %q lost in:\n%s", want, out)
		}
	}
	// 开围栏长于内容里任何反引号串：行首 ``` 与 ```` 都不能提前闭合
	//（最长串按原始内容度量——输出里的围栏自身不算）。
	longest := 0
	for _, l := range strings.Split(md, "\n") {
		if n := len(l) - len(strings.TrimLeft(l, "`")); n > longest {
			longest = n
		}
	}
	fenceLen := len(exporthtml.FenceFor(md))
	if fenceLen <= longest {
		t.Fatalf("fence %d <= content run %d:\n%s", fenceLen, longest, out)
	}
	if n := strings.Count(out, strings.Repeat("`", fenceLen)); n < 2 {
		t.Fatalf("fence lines %d < 2 in:\n%s", n, out)
	}
}

// TestExpandMentionsDirTree（spec 规则 76）：@目录 展开为有界目录树
// ——嵌套层级缩进、目录带尾斜杠、文件+目录混排、深度上限静默、超限
// 截断标注；与文本引用共享总量预算。
func TestExpandMentionsDirTree(t *testing.T) {
	svc, id, ws := mentionEnv(t)
	write := func(name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(ws, filepath.FromSlash(name))), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(name)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a.go")
	write("src/b/util.go")
	write("src/b/c/deep.go")     // 第 3 层文件
	write("src/b/c/d/deeper.go") // 第 4 层：深度上限外
	write("top.md")

	msgs := svc.expandForTest(id, "看下 @src 和 @top.md")
	out := msgs
	if !strings.Contains(out, "src/（目录树") || !strings.Contains(out, "a.go") ||
		!strings.Contains(out, "b/") || !strings.Contains(out, "deep.go") {
		t.Fatalf("dir tree missing entries:\n%s", out)
	}
	if strings.Contains(out, "deeper.go") {
		t.Fatalf("depth cap leaked level 4:\n%s", out)
	}
	// 缩进：b/ 下的文件比 src/ 下多一层缩进。
	if !strings.Contains(out, "\n  util.go") {
		t.Fatalf("indent missing:\n%s", out)
	}
	// 文件引用照常（围栏内容）。
	if !strings.Contains(out, "@top.md：") {
		t.Fatalf("file mention broken:\n%s", out)
	}
}

// TestExpandMentionsDirEntryCap：条目超限截断并标注。
func TestExpandMentionsDirEntryCap(t *testing.T) {
	svc, id, ws := mentionEnv(t)
	dir := filepath.Join(ws, "many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < mentionDirEntries+50; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := svc.expandForTest(id, "扫 @many")
	if !strings.Contains(out, "截断") {
		t.Fatalf("entry cap not annotated:\n%.200s", out)
	}
}

// TestExpandMentionsEmptyDirSkipped：空目录/不可读目录不产生展开（引
// 用保留原样，不产出空树块）。
func TestExpandMentionsEmptyDirSkipped(t *testing.T) {
	svc, id, ws := mentionEnv(t)
	if err := os.MkdirAll(filepath.Join(ws, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := svc.expandForTest(id, "看 @empty")
	if strings.Contains(out, "目录树") {
		t.Fatalf("empty dir expanded:\n%s", out)
	}
	if !strings.Contains(out, "@empty") {
		t.Fatalf("mention token lost:\n%s", out)
	}
}

// expandForTest 是 expandMentions 的文本半边便捷面（测试专用）。
func (s *AgentService) expandForTest(sessionID, text string) string {
	out, _ := s.expandMentions(sessionID, text, nil)
	return out
}
