// Package remotetest is an in-process SSH+SFTP test server,
// shared by the remote client's tests and any tool wired over it.
package remotetest

// An in-process SSH test server: a real x/crypto/ssh server on a
// loopback socket, serving "exec" sessions through the local shell and
// the "sftp" subsystem via pkg/sftp's default local-filesystem server
// rooted at a temp dir. The module's whole test bed — no protocol mocks.

import (
	"bytes"
	"strings"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	ssh "golang.org/x/crypto/ssh"
)

// testServer is one running in-process SSH server.
type Server struct {
	listener net.Listener
	Address  string
	KeyPEM   string // client private key (PEM) for remote.Config.Key
	Root     string // workspace root on disk

	firstShellMu sync.Mutex
	firstShell   string // 交互 shell 收到的首行（客户端 cd 注入探针）
}

// FirstShellLine 返回交互 shell 收到的首行输入（无则空）。真 SSH 服务
// 器的 shell 落在登录 home，客户端必须注入 cd 才能落到工作区根
// （issue #125）；测试服务器把 cwd 设为 root 掩盖了差异，记录首行让
// 注入可断言。
func (s *Server) FirstShellLine() string {
	s.firstShellMu.Lock()
	defer s.firstShellMu.Unlock()
	return s.firstShell
}

// teeFirstLine 把首行（到换行）镜像进 Server，其余原样通过。
func (s *Server) teeFirstLine(r io.Reader) io.Reader {
	return &firstLineTee{srv: s, r: r}
}

type firstLineTee struct {
	srv    *Server
	r      io.Reader
	buffer []byte
	done   bool
}

func (t *firstLineTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 && !t.done {
		t.buffer = append(t.buffer, p[:n]...)
		if i := bytes.IndexByte(t.buffer, '\n'); i >= 0 {
			t.srv.firstShellMu.Lock()
			t.srv.firstShell = strings.TrimRight(string(t.buffer[:i]), "\r ")
			t.srv.firstShellMu.Unlock()
			t.done = true
			t.buffer = nil
		}
	}
	return n, err
}

// Start boots the server on an ephemeral loopback port.
func Start(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()

	// Host key.
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostDER, _ := x509.MarshalPKCS8PrivateKey(hostPriv)
	hostPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: hostDER})
	hostSigner, err := ssh.ParsePrivateKey(hostPEM)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{}
	cfg.AddHostKey(hostSigner)

	// Client key (returned as PEM for Config.Key).
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientDER, _ := x509.MarshalPKCS8PrivateKey(clientPriv)
	clientPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientDER}))
	clientSigner, err := ssh.ParsePrivateKey([]byte(clientPEM))
	if err != nil {
		t.Fatal(err)
	}
	cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
			return &ssh.Permissions{}, nil
		}
		return nil, fmt.Errorf("unknown key")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{listener: ln, Address: ln.Addr().String(), KeyPEM: clientPEM, Root: root}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(srv, conn, cfg, root)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return srv
}

// serveConn handles one SSH connection's channels.
func serveConn(srv *Server, conn net.Conn, cfg *ssh.ServerConfig, root string) {
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go serveSession(srv, channel, requests, root)
	}
}

// serveSession answers exec and sftp-subsystem requests.
func serveSession(srv *Server, channel ssh.Channel, requests <-chan *ssh.Request, root string) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "exec":
			var payload struct{ Command string }
			ssh.Unmarshal(req.Payload, &payload)
			req.Reply(true, nil)
			line := stripCdPrefix(payload.Command)
			cmd := shellCommand(line)
			cmd.Dir = root
			// 流式直写通道而非攒齐再写（后台任务的输出要边跑边读）；
			// 客户端断开即杀——对齐真 SSH 服务器的会话语义，后台
			// KillTree（关通道）依赖它。
			cmd.Stdout = channel
			cmd.Stderr = channel
			if err := cmd.Start(); err != nil {
				channel.SendRequest("exit-status", false,
					ssh.Marshal(struct{ Status uint32 }{uint32(127)}))
				return
			}
			// 整树进 kill-on-close 的 Job Object（Windows）：关句柄即
			// 全树同步死亡——cmd.Wait 只等直接子进程，孙进程的死亡
			// 时序不能靠 taskkill 的异步宽限（负载下真实竞态过）。
			tree, _ := attachTreeJob(cmd)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			disconnected := make(chan struct{})
			go func() {
				for range requests { // 通道关闭（requests 收尾）即感知断开
				}
				close(disconnected)
			}()
			select {
			case err := <-done:
				if tree != nil {
					tree.kill() // 命令已退：收拾漏网的孙进程
				}
				code := 0
				if err != nil {
					code = exitCodeOf(err)
				}
				channel.SendRequest("exit-status", false,
					ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
				return
			case <-disconnected:
				if tree != nil {
					tree.kill()
				} else {
					killProcessTree(cmd.Process)
				}
				<-done
				return
			}
		case "pty-req":
			// 无真 PTY：记录接受即可（测试服务器按行回环）。
			req.Reply(true, nil)
		case "shell":
			req.Reply(true, nil)
			cmd := interactiveShell()
			cmd.Dir = root
			cmd.Stdin = srv.teeFirstLine(channel)
			cmd.Stdout = channel
			cmd.Stderr = channel
			runErr := cmd.Run()
			code := 0
			if runErr != nil {
				code = exitCodeOf(runErr)
			}
			channel.SendRequest("exit-status", false,
				ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
			return
		case "subsystem":
			var payload struct{ Subsystem string }
			ssh.Unmarshal(req.Payload, &payload)
			if payload.Subsystem != "sftp" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			server, err := sftp.NewServer(channel)
			if err != nil {
				return
			}
			server.Serve()
			server.Close()
			return
		default:
			req.Reply(false, nil)
		}
	}
}

// interactiveShell is the platform shell for shell sessions（无 PTY 的
// 行式桥接：回显由终端模拟器本地完成）.
func interactiveShell() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd")
	}
	return exec.Command("sh")
}

// shellCommand builds the platform shell invocation.
func shellCommand(line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", line)
	}
	return exec.Command("sh", "-c", line)
}

// killProcessTree terminates a command's whole tree on disconnect: cmd
// wraps the line in cmd.exe / sh, whose children (ping、sleep…) would
// otherwise outlive the channel close and pin the server's temp dir
// (真 SSH 服务器同样杀整组——OpenSSH 对会话进程组发 SIGHUP）.
func killProcessTree(p *os.Process) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
		return
	}
	_ = p.Kill()
}

// exitCodeOf extracts an ExitError's code.
func exitCodeOf(err error) int {
	type exitError interface{ ExitCode() int }
	if ee, ok := err.(exitError); ok {
		return ee.ExitCode()
	}
	return 1
}

// stripCdPrefix drops the jail "cd '<root>' && " the client prepends —
// the server already runs in the root (test-bed only).
func stripCdPrefix(command string) string {
	if i := indexIn(command, " && "); i >= 0 && startsWithCd(command) {
		return command[i+4:]
	}
	return command
}

func startsWithCd(s string) bool { return len(s) >= 2 && s[0:2] == "cd" }

func indexIn(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
