package uiapp

import (
	"io"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// 终端面板（ZCode browser-workbench 的 terminal-ui 半边）：底部抽屉里跑
// 工作区的 shell（mygo terminal 插件，Ghostty 的 libghostty-vt）。终端与
// 会话工作区绑定：切换会话即换目录重建。库缺失时降级为提示，不崩。
type termFactory func(dir string) (*terminal.Terminal, error)

// newTerminal is the production factory（测试注入替身，避免真拉 DLL）.
var newTerminal termFactory = func(dir string) (*terminal.Terminal, error) {
	return terminal.New(terminal.Options{Dir: dir})
}

// newTerminalConn is the remote factory: the terminal runs over an SSH
// shell Conn（测试注入替身）.
var newTerminalConn = func(conn io.ReadWriteCloser) (*terminal.Terminal, error) {
	return terminal.New(terminal.Options{Conn: conn})
}

// terminalPanelView is the bottom drawer between chat and composer.
func (a *App) terminalPanelView(c *ui.Context, t *ui.Theme) {
	if !a.termOpen {
		return
	}
	ui.Column(c).Height(300).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Padding(6, 12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "终端 · "+a.termDir).Bold().FontSize(13)
			ui.Spacer(c)
			if ui.Button(c, "✕").Clicked() {
				a.ToggleTerminal()
			}
		})
		if a.term == nil {
			ui.Text(c, "终端不可用："+a.termErr).TextColor(t.Danger).Padding(12, 16)
			return
		}
		terminal.View(c, a.term).Fill()
	})
}

// ToggleTerminal opens or closes the panel, (re)creating the terminal for
// the active session's workspace as needed.
func (a *App) ToggleTerminal() {
	if a.termOpen {
		a.termOpen = false
		a.closeTerminal()
		return
	}
	if a.activeID == "" {
		return
	}
	if err := a.ensureTerminal(); err != nil {
		a.termErr = err.Error()
	}
	a.termOpen = true
}

// ensureTerminal binds a live terminal to the active workspace, replacing
// one spawned for a different directory.
func (a *App) ensureTerminal() error {
	dir := a.svc.SessionWorkspace(a.activeID)
	if a.term != nil && a.termDir == dir && dir != "" {
		return nil
	}
	a.closeTerminal()
	if dir == "" {
		a.termErr = "会话没有本地工作区"
		return nil
	}
	if strings.HasPrefix(dir, "ssh://") {
		conn, err := a.svc.TerminalConn(a.activeID)
		if err != nil {
			a.term = nil
			a.termErr = err.Error()
			return err
		}
		if conn == nil {
			// 拨号失败退化为本地工作区：没有 shell 可接（workspaceFor 的
			// 降级语义）。
			a.termErr = "SSH 工作区连接失败，无法打开远程终端"
			return nil
		}
		term, err := newTerminalConn(conn)
		if err != nil {
			conn.Close()
			a.term = nil
			a.termErr = err.Error()
			return err
		}
		a.term, a.termDir, a.termErr = term, dir, ""
		a.termConn = conn
		return nil
	}
	term, err := newTerminal(dir)
	if err != nil {
		a.term = nil
		a.termErr = err.Error()
		return err
	}
	a.term, a.termDir, a.termErr = term, dir, ""
	return nil
}

func (a *App) closeTerminal() {
	if a.term != nil {
		a.term.Close()
		a.term = nil
	}
	if a.termConn != nil {
		a.termConn.Close()
		a.termConn = nil
	}
}

// CloseTerminalOnExit tears the terminal down at app exit（PTY 子进程
// 不能留给系统收尾）.
func (a *App) CloseTerminalOnExit() {
	a.closeTerminal()
}
