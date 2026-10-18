package uiapp

// 贴图（issue #216）：addImageAttachment 是 📎 与 Ctrl+V 共用的附件通
// 道——单张/总量上限与 notice 形态在此断言；PasteImage 的无图放行（不
// 报错、不吞文本）在无窗口测试环境只能覆盖 ErrNoImage 分支，真实剪贴
// 板路径见 PR 手动验证记录。

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/clipimg"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// errNoImageForTest 即 clipimg.ErrNoImage（经变量比较隔离包细节）。
var errNoImageForTest = clipimg.ErrNoImage

func newPasteApp(t *testing.T) *App {
	t.Helper()
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	a := New(svc)
	a.activeID = "sess" // 贴图与会话绑定
	return a
}

// TestAddImageAttachmentCaps：单张超 5MB 拒绝；总量超预算拒绝且 notice
// 带文件名（📎 与贴图同一条通道，形态一致）。
func TestAddImageAttachmentCaps(t *testing.T) {
	a := newPasteApp(t)
	if !a.addImageAttachment("a.png", "image/png", []byte("x")) {
		t.Fatal("small image rejected")
	}
	if len(a.attach) != 1 || a.attach[0].Name != "a.png" ||
		a.attach[0].Block.Media != "image/png" {
		t.Fatalf("attachment: %+v", a.attach)
	}
	if a.addImageAttachment("big.png", "image/png", make([]byte, attachmentCapBytes+1)) {
		t.Fatal("oversized single accepted")
	}
	notices := a.noticeTexts()
	if !strings.Contains(notices, "big.png") || !strings.Contains(notices, "上限") {
		t.Fatalf("single-cap notice: %q", notices)
	}
	// 总量：贴到只剩预算不足（attachmentTotalCapBytes 是编码后总量）。
	a.attach = nil
	a.addImageAttachment("half1.png", "image/png", make([]byte, 3<<20))
	if a.addImageAttachment("half2.png", "image/png", make([]byte, 4<<20)) {
		t.Fatal("total-cap overflow accepted")
	}
	if last := a.noticeTexts(); !strings.Contains(last, "half2.png") ||
		!strings.Contains(last, "分条发送") {
		t.Fatalf("total-cap notice: %q", last)
	}
}

// TestPasteImageNoClipboardPassesThrough：无图（ErrNoImage 分支，注入
// 读取点隔离真机剪贴板状态）返回 false，不产生 notice、不动附件；命
// 中时成附件（PNG 命名）。运行中一律拒绝（与 Attach 同一守卫）。
func TestPasteImageNoClipboardPassesThrough(t *testing.T) {
	old := readClipboardImage
	t.Cleanup(func() { readClipboardImage = old })
	readClipboardImage = func() ([]byte, error) { return nil, errNoImageForTest }

	a := newPasteApp(t)
	if a.PasteImage() {
		t.Fatal("paste claimed without a usable image")
	}
	if len(a.attach) != 0 || a.noticeTexts() != "" {
		t.Fatalf("no-image paste left state: attach=%d notices=%q",
			len(a.attach), a.noticeTexts())
	}

	// 命中路径：注入 PNG 字节 → 附件带「粘贴图片」名。
	readClipboardImage = func() ([]byte, error) { return []byte{0x89, 'P', 'N', 'G'}, nil }
	if !a.PasteImage() {
		t.Fatal("paste with image rejected")
	}
	if len(a.attach) != 1 || !strings.HasPrefix(a.attach[0].Name, "粘贴图片") ||
		a.attach[0].Block.Media != "image/png" {
		t.Fatalf("attachment: %+v", a.attach)
	}

	// 运行中一律拒绝。
	a.chat.running = true
	readClipboardImage = func() ([]byte, error) { return []byte{0x89, 'P'}, nil }
	if a.PasteImage() {
		t.Fatal("paste accepted while running")
	}
}

// noticeTexts 收集当前 notice 文本（断言用）。
func (a *App) noticeTexts() string {
	var b strings.Builder
	for _, it := range a.chat.items {
		if it.kind == itemNotice {
			b.WriteString(it.text + "\n")
		}
	}
	return b.String()
}
