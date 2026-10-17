package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestAskModalShowsPreviewDiff: a pending ask with a preview diff renders
// it in the modal; answering closes it.
func TestAskModalShowsPreviewDiff(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(info.ID)
	tt.Frame()

	// 模拟一次挂起的询问（asker 在 loop goroutine 上阻塞等应答）。
	go a.asker(agent.AskRequest{Tool: "write", Args: "path: a.txt",
		Reason: "no matching rule", Diff: "@@ -1 +1 @@\n-old line\n+new content\n"})
	waitFrame(t, tt, "将做出的改动")
	if !tt.HasText("+new content") || !tt.HasText("-old line") {
		t.Fatal("diff not rendered in the ask modal")
	}

	if err := tt.Click("拒绝"); err != nil {
		t.Fatalf("deny: %v", err)
	}
	tt.Frame()
	if a.askOpen {
		t.Fatal("modal did not close")
	}
}

// waitFrame pumps frames until the text appears（asker 异步置位）.
func waitFrame(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		tt.Frame()
		if tt.HasText(text) {
			return
		}
	}
	t.Fatalf("%q never appeared", text)
}
