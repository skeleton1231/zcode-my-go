package remote

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// BgProcess 是一个远端后台命令（后台 shell 的远程形态）：Start 后不
// 阻塞回合，输出经 os.Pipe 流式可读（与 Exec 同款防丢尾教训——库内
// 管道在会话退出时丢尾部）。KillTree 关闭会话通道（SSH 服务器随通道
// 关闭对会话进程组发 SIGHUP），并在拿到 pid 时补一条确证击杀——SIGHUP
// 只覆盖会话进程组，setsid/双 fork daemon 会脱离进程组存活（issue
// #166）：本地 Job Object 无条件杀整树，远程此前只关通道，jobs 面板
// 显示已终止而进程仍在远端跑。SSH 本质上没有 Job Object，完全对等做
// 不到——本实现的目标是"确证 + 可见"：尽力补杀并让残留可能留痕。
// 方法集与 tools.BgProcess 同构，tools 不 import 本包（RemoteFS 镜像
// 约定）。
type BgProcess struct {
	sess   *ssh.Session
	client *Client // KillTree 的确证击杀经独立 exec 通道
	pw     *os.File // 合并输出的写端；Wait 后关闭让读者收 EOF
	pr     *os.File // 输出读端（调用方排空）
	line   string   // 已带 cd 根目录的完整命令
	posix  bool     // 远端是 POSIX shell（pid 包装与确证击杀的前提）

	// 首行扫描假定单读者（tools 侧 io.Copy 排空）；mu 只保护 pid 供
	// KillTree 在另一 goroutine 并发读取。
	mu      sync.Mutex
	pid     int
	scanned bool
	scanBuf []byte // 首行扫描的待回放余量
}

// pidMarker 首行回传 shell pid；POSIX 远端的命令经 exec 继承同一 pid
// （shell 即进程组长，kill 组长即杀全组）。标记行在读者处吞掉——模型
// 不见 pid 噪音。
const pidMarker = "ZGO_BG_PID="

// pidScanCap bounds the first-line scan: 首行超过此长度仍无换行即放弃
// 匹配、原样回放（防御远端意外形态，不阻塞长输出）。
const pidScanCap = 128

// StartBackground 启动一个不等待完成的后台命令：cd 到工作区根后执行，
// stdout/stderr 合并。远程后台与本地后台同语义（同一 job 注册表）。
// POSIX 远端会包装命令回传 pid（见 pidMarker）；cmd/PowerShell 远端不
// 包装（exec 非内建、$$ 不展开，包装会弄坏命令）。
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
	posix := c.posixShell()
	line := "cd " + quote(c.cfg.Root) + " && " + command
	if posix {
		line = wrapPosixBg(c.cfg.Root, command)
	}
	return &BgProcess{
		sess: session, client: c, pw: pw, pr: pr,
		line: line, posix: posix,
	}, nil
}

// wrapPosixBg builds the pid-reporting wrapper: 标记行最先输出，exec 让
// 命令继承 shell 的 pid（进程组长）。
func wrapPosixBg(root, command string) string {
	return "cd " + quote(root) + " && echo " + pidMarker + "$$ && exec " + command
}

// posixShell probes the remote shell once（cmd 原样回显 "$$"，sh 展开
// 成 pid 数字；PowerShell 的 $$ 是自动变量也不会是数字形态）。
func (c *Client) posixShell() bool {
	c.posixOnce.Do(func() {
		res, err := c.Exec("echo zgo_$$")
		if err != nil {
			return // 探测失败按非 POSIX 降级（不包装、不确证）
		}
		if pidProbeRe.FindStringSubmatch(strings.TrimSpace(res.Output)) != nil {
			c.posix = true
		}
	})
	return c.posix
}

var pidProbeRe = regexp.MustCompile(`zgo_(\d+)`)

// Start launches the remote command (does not wait).
func (p *BgProcess) Start() error { return p.sess.Start(p.line) }

// Wait blocks until the remote command exits, then closes the write end
// so the output reader sees EOF.
func (p *BgProcess) Wait() error {
	err := p.sess.Wait()
	p.pw.Close()
	return err
}

// KillTree terminates the remote process: 关闭会话通道（SIGHUP 覆盖会
// 话进程组），拿到 pid 时经独立 exec 通道补一条确证击杀（进程组 + 组
// 长 + 直系子进程，覆盖普通脱离进程组者）；双 fork daemon 挂到 init
// 名下仍杀不到——记日志让残留可见。确证尽力而为：失败不影响通道关闭
// 的既有语义。
func (p *BgProcess) KillTree() error {
	err := p.sess.Close()
	p.pw.Close()
	// EOF 是"通道已在关闭中"——击杀目的已达成（Linux 服务器在断开
	// 竞态下 Close 常回 io.EOF，CI 首跑实测），按成功处理。
	if err != nil && errors.Is(err, io.EOF) {
		err = nil
	}
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid == 0 {
		if p.posix {
			log.Printf("zgo: remote background job killed without captured pid; daemonized survivors possible")
		}
		return err
	}
	if _, kerr := p.client.Exec(confirmKillLine(pid)); kerr != nil {
		log.Printf("zgo: remote kill confirm for pid %d failed: %v", pid, kerr)
	}
	return err
}

// confirmKillLine 覆盖三类目标：进程组（kill -- -pid，组长即 shell），
// 组长本人（脱离组者的父），直系子进程（pkill -P，setsid 脱组仍在树
// 上）；全部静默容错——目标是尽力补杀，不是逐个报成败。
func confirmKillLine(pid int) string {
	return fmt.Sprintf("kill -TERM -- -%d 2>/dev/null; kill -TERM %d 2>/dev/null; pkill -TERM -P %d 2>/dev/null; true", pid, pid, pid)
}

// bgReader 是 Stdout 的首行标记过滤器：POSIX 形态下吞掉 pid 标记行、
// 回放其余字节；非标记或超长首行原样透传。
type bgReader struct{ p *BgProcess }

func (r *bgReader) Read(b []byte) (int, error) {
	p := r.p
	if p.posix {
		p.scanOnce() // 单读者约定（io.Copy 排空）
	}
	if len(p.scanBuf) > 0 {
		n := copy(b, p.scanBuf)
		p.scanBuf = p.scanBuf[n:]
		return n, nil
	}
	return p.pr.Read(b)
}

func (r *bgReader) Close() error { return r.p.pr.Close() }

// scanOnce reads until the first newline (capped) and records the pid if
// the line is the marker; scanBuf keeps everything that must replay.
func (p *BgProcess) scanOnce() {
	if p.scanned {
		return
	}
	p.scanned = true
	var buf []byte
	tmp := make([]byte, 64)
	for len(buf) < pidScanCap {
		want := pidScanCap - len(buf)
		if want > len(tmp) {
			want = len(tmp)
		}
		n, err := p.pr.Read(tmp[:want])
		buf = append(buf, tmp[:n]...)
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			if pid, ok := parsePidMarker(buf[:i]); ok {
				p.mu.Lock()
				p.pid = pid
				p.mu.Unlock()
				buf = append([]byte(nil), buf[i+1:]...) // 标记行吞掉
			}
			break // 非标记：整段（含首行）回放
		}
		if err != nil {
			break // EOF/错误：无标记，全部回放
		}
	}
	p.scanBuf = buf
}

// parsePidMarker accepts exactly "ZGO_BG_PID=<positive digits>"（CRLF
// 容忍——真 SSH 服务器有的走 \r\n）。
func parsePidMarker(line []byte) (int, bool) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	if !bytes.HasPrefix(line, []byte(pidMarker)) {
		return 0, false
	}
	pid, err := strconv.Atoi(string(line[len(pidMarker):]))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// Stdout returns the combined output stream (close it when drained).
// 返回 io.ReadCloser 以精确匹配 tools.BgProcess 的方法集（结构化实现
// 要求签名一致，*os.File 的 narrower 返回类型不算实现）。
func (p *BgProcess) Stdout() io.ReadCloser { return &bgReader{p: p} }
