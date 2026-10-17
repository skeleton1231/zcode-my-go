package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestCrossSearchModalFlow: typing a query and searching renders hits;
// clicking one switches the session and seeds the in-session filter.
func TestCrossSearchModalFlow(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	other, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AppendForTesting(other.ID, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "部署脚本 deploy.sh 出错了"}}}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(current.ID)
	tt.Frame()

	a.OpenCrossSearch()
	tt.Frame()
	if !tt.HasText("搜索所有会话") {
		t.Fatal("modal not rendered")
	}

	a.cross.query = "deploy.sh"
	a.RunCrossSearch()
	// 后台扫描经 a.update 回视图线程：泵帧直到结果落地。
	deadline := time.Now().Add(3 * time.Second)
	for len(a.cross.results) == 0 && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if len(a.cross.results) == 0 {
		t.Fatalf("no results: %+v", a.cross)
	}
	tt.Frame()
	if !tt.HasText("deploy.sh") {
		t.Fatal("hit not rendered")
	}

	// 点命中（按钮文本是完整标签「标题 — 片段」，标题可能由首条消息
	// 派生）：按实际结果构造标签再点。
	hit := a.cross.results[0]
	label := hit.Title
	if label == "" {
		label = "（未命名）"
	}
	label += " — " + hit.Snippet
	if err := tt.Click(label); err != nil {
		t.Fatalf("hit %q not clickable: %v", label, err)
	}
	if a.activeID != other.ID {
		t.Fatalf("active = %s, want %s", a.activeID, other.ID)
	}
	if !a.searchOpen || a.searchQuery != "deploy.sh" {
		t.Fatalf("in-session filter = %q open=%v", a.searchQuery, a.searchOpen)
	}
}
