package remote

import (
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
)

// ShellConn is an interactive remote shell over an SSH PTY（终端面板的
// 远程形态——mygo terminal 插件的 Conn：读端显示、写端键入，Resize 经
// WindowChange 传播）。
type ShellConn struct {
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout *os.File // os.Pipe 读端：会话退出时写端关闭，读者收到 EOF
	mu     sync.Mutex
	closed bool
}

// Shell opens an interactive login shell in the client's root with an
// xterm-256color PTY (80x24 initially; the terminal resizes it).
func (c *Client) Shell() (*ShellConn, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	if err := sess.RequestPty("xterm-256color", 80, 24, ssh.TerminalModes{}); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	// 输出走 os.Pipe：session.Wait 返回后写端关闭，读端干净 EOF（与
	// Exec 同款教训：直接用库内管道会在退出时丢尾部输出）。
	pr, pw, err := os.Pipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	sess.Stdout = pw
	sess.Stderr = pw
	if err := sess.Shell(); err != nil {
		pr.Close()
		pw.Close()
		sess.Close()
		return nil, err
	}
	// 真 SSH 服务器的 shell 落在登录 home（issue #125：注释曾称 in
	// the client's root 而实现无任何落地处理，与本地终端和标题栏不
	// 符）。Shell 请求无法携带命令，PTY 起来后经 stdin 注入一次 cd
	// ——远端 shell 会执行它；注入失败视为会话失败。
	if _, werr := stdin.Write([]byte("cd " + quote(c.cfg.Root) + "\n")); werr != nil {
		pr.Close()
		pw.Close()
		stdin.Close()
		sess.Close()
		return nil, fmt.Errorf("cd into workspace root: %w", werr)
	}
	go func() {
		sess.Wait()
		pw.Close()
	}()
	return &ShellConn{sess: sess, stdin: stdin, stdout: pr}, nil
}

// Read implements io.Reader（会话结束后 EOF）.
func (s *ShellConn) Read(p []byte) (int, error) { return s.stdout.Read(p) }

// Write implements io.Writer（键入直达远端 shell）.
func (s *ShellConn) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Resize relays the terminal's size（插件按需探测的可选方法）.
func (s *ShellConn) Resize(cols, rows int) error {
	return s.sess.WindowChange(rows, cols)
}

// Close ends the session（幂等）.
func (s *ShellConn) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.stdin.Close()
	s.stdout.Close()
	return s.sess.Close()
}
