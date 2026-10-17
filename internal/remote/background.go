package remote

import (
	"io"
	"os"

	"golang.org/x/crypto/ssh"
)

// BgProcess 是一个远端后台命令（后台 shell 的远程形态）：Start 后不
// 阻塞回合，输出经 os.Pipe 流式可读（与 Exec 同款防丢尾教训——库内
// 管道在会话退出时丢尾部）。KillTree 即关闭会话通道：SSH 服务器随通
// 道关闭终止远端进程（OpenSSH 对会话进程组发 SIGHUP）。方法集与
// tools.BgProcess 同构，tools 不 import 本包（RemoteFS 镜像约定）。
type BgProcess struct {
	sess *ssh.Session
	pw   *os.File // 合并输出的写端；Wait 后关闭让读者收 EOF
	pr   *os.File // 输出读端（调用方排空）
	line string   // 已带 cd 根目录的完整命令
}

// StartBackground 启动一个不等待完成的后台命令：cd 到工作区根后执行，
// stdout/stderr 合并。远程后台与本地后台同语义（同一 job 注册表）。
func (c *Client) StartBackground(command string) (*BgProcess, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	session.Stdout = pw
	session.Stderr = pw
	return &BgProcess{
		sess: session, pw: pw, pr: pr,
		line: "cd " + quote(c.cfg.Root) + " && " + command,
	}, nil
}

// Start launches the remote command (does not wait).
func (p *BgProcess) Start() error { return p.sess.Start(p.line) }

// Wait blocks until the remote command exits, then closes the write end
// so the output reader sees EOF.
func (p *BgProcess) Wait() error {
	err := p.sess.Wait()
	p.pw.Close()
	return err
}

// KillTree terminates the remote process by closing the session channel.
func (p *BgProcess) KillTree() error {
	err := p.sess.Close()
	p.pw.Close()
	return err
}

// Stdout returns the combined output stream (close it when drained).
// 返回 io.ReadCloser 以精确匹配 tools.BgProcess 的方法集（结构化实现
// 要求签名一致，*os.File 的 narrower 返回类型不算实现）。
func (p *BgProcess) Stdout() io.ReadCloser { return p.pr }
