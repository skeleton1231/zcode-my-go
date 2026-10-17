package uiapp

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestTerminalPanelLifecycle: toggle drives the factory against the
// session's workspace; factory failures degrade to a notice; retries are
// not cached; toggling off closes; ssh workspaces refuse clearly.
// （终端渲染面由 mygo terminal 插件自测；成功路径的 New 无法在无 DLL 的
// 测试环境里真跑，工厂注入覆盖生命周期逻辑。）
func TestTerminalPanelLifecycle(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	restore := newTerminal
	newTerminal = func(dir string) (*terminal.Terminal, error) {
		calls++
		return nil, errors.New("libghostty 缺失")
	}
	defer func() { newTerminal = restore }()

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(info.ID)
	tt.Frame()

	a.ToggleTerminal()
	tt.Frame()
	if !a.termOpen || a.termErr == "" {
		t.Fatalf("state = open=%v err=%q", a.termOpen, a.termErr)
	}
	if calls != 1 {
		t.Fatalf("factory calls = %d", calls)
	}
	if !tt.HasText("终端不可用") {
		t.Fatal("degraded notice not shown")
	}

	// 再开关一次：失败不缓存，重试工厂。
	a.ToggleTerminal()
	a.ToggleTerminal()
	if calls != 2 {
		t.Fatalf("retry calls = %d", calls)
	}
	a.ToggleTerminal()
	if a.termOpen {
		t.Fatal("still open")
	}

	// SSH 远程工作区：明确拒绝（NewSession 不拨号，仅记录目标）。
	ssh, err := svc.NewSession("ssh://user@localhost:1/tmp")
	if err != nil {
		t.Skipf("ssh session creation: %v", err)
	}
	a.OpenSession(ssh.ID)
	a.ToggleTerminal()
	if !strings.Contains(a.termErr, "SSH") {
		t.Fatalf("ssh refusal missing: %q", a.termErr)
	}
	if calls != 2 { // ssh 路径不触工厂
		t.Fatalf("factory called for ssh: %d", calls)
	}
	a.ToggleTerminal()
}
