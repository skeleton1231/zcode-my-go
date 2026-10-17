package uiapp

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestTerminalSSHBranchWiring: an ssh session's terminal panel goes
// through the Conn factory with a live remote shell（工厂注入捕获连接；
// 终端对象无法在无 DLL 环境伪造，返回错误走降级——连接路径即被验证）.
func TestTerminalSSHBranchWiring(t *testing.T) {
	srv := remotetest.Start(t)
	t.Setenv("ZGO_SSH_KEY", srv.KeyPEM)
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ssh, err := svc.NewSession("ssh://t@" + srv.Address + "/" + filepath.ToSlash(srv.Root))
	if err != nil {
		t.Skipf("ssh session: %v", err)
	}

	var captured io.ReadWriteCloser
	restoreLocal, restoreConn := newTerminal, newTerminalConn
	newTerminal = func(dir string) (*terminal.Terminal, error) {
		return nil, errors.New("不应走本地工厂")
	}
	newTerminalConn = func(conn io.ReadWriteCloser) (*terminal.Terminal, error) {
		captured = conn
		return nil, errors.New("无 DLL")
	}
	defer func() { newTerminal, newTerminalConn = restoreLocal, restoreConn }()

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(ssh.ID)
	tt.Frame()

	a.ToggleTerminal()
	tt.Frame()
	if captured == nil {
		t.Fatal("conn factory not called for ssh session")
	}
	if !a.termOpen || a.termErr == "" {
		t.Fatalf("state = open=%v err=%q", a.termOpen, a.termErr)
	}
	// 工厂报错分支已把连接关闭（Close 幂等）；连接路径由 captured 证毕。
	if err := captured.Close(); err != nil {
		t.Fatalf("conn close: %v", err)
	}
	a.ToggleTerminal()
}
