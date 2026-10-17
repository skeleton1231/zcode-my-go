package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// seedTurns boots an app with a session whose transcript has three user
// turns (each with an assistant answer).
func seedTurns(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(info.ID)
	for _, q := range []string{"第一问", "第二问", "第三问"} {
		a.chat.appendUser(q)
		a.chat.items = append(a.chat.items, chatItem{kind: itemAssistant, text: "答"})
	}
	tt.Frame()
	return a, tt
}

// TestJumpTurnNavigation: jumpTurn walks user turns downward from the
// end (no-op), upward twice, then back down; boundaries don't move.
func TestJumpTurnNavigation(t *testing.T) {
	a, _ := seedTurns(t)
	if len(a.chat.items) != 6 {
		t.Fatalf("items = %d", len(a.chat.items))
	}
	// 从末尾向上跳：应到第三问（行 4，最后一个用户回合）。
	a.jumpTurn(-1)
	if a.navRow != 4 || a.chat.items[a.navRow].text != "第三问" {
		t.Fatalf("first jump navRow=%d", a.navRow)
	}
	if a.list.FollowEnd {
		t.Fatal("jump did not stop following")
	}
	// 再向上：第二问（行 2），再向上：第一问（行 0）。
	a.jumpTurn(-1)
	if a.navRow != 2 {
		t.Fatalf("second jump navRow=%d", a.navRow)
	}
	a.jumpTurn(-1)
	if a.navRow != 0 {
		t.Fatalf("third jump navRow=%d", a.navRow)
	}
	// 边界：再向上不动。
	a.jumpTurn(-1)
	if a.navRow != 0 {
		t.Fatalf("boundary moved: %d", a.navRow)
	}
	// 向下回来。
	a.jumpTurn(1)
	if a.navRow != 2 {
		t.Fatalf("down jump navRow=%d", a.navRow)
	}
}
