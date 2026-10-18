// Package remote is the SSH workspace client: dial a host once, run
// commands over exec channels, and touch files over SFTP. Targets parse
// from ssh://user@host:port/path URLs (ZCode's remote workspaces).
package remote

import (
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Config describes one SSH workspace target.
type Config struct {
	User     string // default: current user
	Host     string
	Port     string // default: 22
	Password string
	// Key is a PEM private key; when empty and no password, the default
	// agent / ~/.ssh/id_rsa chain is tried.
	Key string

	// Root is the workspace directory on the host (the path component of
	// the target URL); file operations resolve inside it.
	Root string
}

// String renders the target back (without secrets).
func (c Config) String() string {
	host := c.Host
	if c.Port != "" && c.Port != "22" {
		host = net.JoinHostPort(c.Host, c.Port)
	}
	if c.User != "" {
		host = c.User + "@" + host
	}
	return "ssh://" + host + c.Root
}

// isASCIILetter reports a single-byte ASCII letter (drive-letter check).
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// ParseTarget parses ssh://user@host:port/path (ZCode's remote workspace
// address); an empty scheme means a local workspace and errors here.
func ParseTarget(target string) (Config, error) {
	if !strings.HasPrefix(target, "ssh://") {
		return Config{}, fmt.Errorf("not an ssh target: %q", target)
	}
	rest := strings.TrimPrefix(target, "ssh://")
	root := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		root = rest[i:]
		rest = rest[:i]
	}
	cfg := Config{Root: path.Clean("/" + root)}
	// Windows 盘符根（"/C:/…"）：真 SSH 到 Windows 主机的路径不带前导
	// 斜杠，URL 解析还原时要剥掉——否则 "/C:/…" 在任何一侧的 fs 上都
	// 无法解析（服务层 openWorkspace 即经 ParseTarget 走这条路）。
	if n := len(cfg.Root); n >= 3 && cfg.Root[0] == '/' && cfg.Root[2] == ':' &&
		(isASCIILetter(cfg.Root[1])) {
		cfg.Root = cfg.Root[1:]
	}
	if rest == "" {
		return Config{}, fmt.Errorf("ssh target needs a host")
	}
	if host, port, err := net.SplitHostPort(rest); err == nil {
		cfg.Host, cfg.Port = host, port
	} else {
		cfg.Host = rest
	}
	if i := strings.IndexByte(cfg.Host, '@'); i >= 0 {
		cfg.User, cfg.Host = cfg.Host[:i], cfg.Host[i+1:]
	}
	if cfg.Host == "" {
		return Config{}, fmt.Errorf("ssh target needs a host")
	}
	return cfg, nil
}

// dialTimeout bounds the TCP+SSH handshake.
const dialTimeout = 10 * time.Second

// Client is one dialed SSH workspace.
type Client struct {
	cfg    Config
	conn   *ssh.Client
	sftp   *sftp.Client
	closer io.Closer

	// 远端 shell 形态一次性探测（后台 pid 包装与确证击杀的前提，
	// issue #166）：cmd/PowerShell 不包装（exec 非内建、$$ 不展开）。
	posixOnce sync.Once
	posix     bool
	// keepalive/reconnect state（issue #220）。
	ka   keepAliveState
	kaOn sync.Once
}

// Dial connects and opens the SFTP subsystem.
func Dial(cfg Config) (*Client, error) {
	var auths []ssh.AuthMethod
	if cfg.Password != "" {
		auths = append(auths, ssh.Password(cfg.Password))
	}
	if cfg.Key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.Key))
		if err != nil {
			return nil, fmt.Errorf("parsing key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if len(auths) == 0 {
		// v1：无显式凭据时不猜默认密钥——测试与显式配置都给出凭据；
		// agent/密钥链在工具接线轮补。
		_ = tryDefaultIdentities
	}
	port := cfg.Port
	if port == "" {
		port = "22"
	}
	user := cfg.User
	if user == "" {
		user = os.Getenv("USERNAME")
		if user == "" {
			user = os.Getenv("USER")
		}
	}
	conn, err := ssh.Dial("tcp", net.JoinHostPort(cfg.Host, port), &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // v1：工作区场景先通，后续按 known_hosts 收紧
		Timeout:         dialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", cfg.String(), err)
	}
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("opening sftp: %w", err)
	}
	c := &Client{cfg: cfg, conn: conn, sftp: sftpClient, closer: sftpClient}
	c.ka.alive.Store(true)
	c.StartKeepAlive() // issue #220
	return c, nil
}

// dialTarget/dialUser/dialAuths are the Dial argument pieces (reconnect reuses).
func dialTarget(cfg Config) string {
	port := cfg.Port
	if port == "" {
		port = "22"
	}
	return net.JoinHostPort(cfg.Host, port)
}

func dialUser(cfg Config) string {
	if cfg.User != "" {
		return cfg.User
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return os.Getenv("USER")
}

func dialAuths(cfg Config) []ssh.AuthMethod {
	var auths []ssh.AuthMethod
	if cfg.Password != "" {
		auths = append(auths, ssh.Password(cfg.Password))
	}
	if cfg.Key != "" {
		if signer, err := ssh.ParsePrivateKey([]byte(cfg.Key)); err == nil {
			auths = append(auths, ssh.PublicKeys(signer))
		}
	}
	return auths
}

// Close shuts the session down.
func (c *Client) Close() error {
	if c.closer != nil {
		c.closer.Close()
	}
	return c.conn.Close()
}

// String is the target (no secrets).
func (c *Client) String() string { return c.cfg.String() }

// execTimeout caps each command.
const execTimeout = 10 * time.Minute

// ExecResult is one command's outcome.
type ExecResult struct {
	Output   string // combined stdout+stderr, in arrival order
	ExitCode int
}

// Exec runs one command line in the workspace root.
// execOutputCap 限制一次远程命令读入内存的输出（AGENTS.md：工具输出
// 必须截断；远程 cat 大日志是模型可直达路径，issue #84）。
const execOutputCap = 1 << 20 // 1MB

func (c *Client) Exec(command string) (ExecResult, error) {
	if err := c.ensureConnected(); err != nil {
		return ExecResult{}, err // issue #220
	}
	session, err := c.conn.NewSession()
	if err != nil {
		return ExecResult{}, err
	}
	defer session.Close()
	// Run() 收到 exit-status 即返回，而 Stdout 搬运 goroutine 可能还没写完
	// （实测丢尾部数据）；用 os.Pipe 让库同步写 fd，结束后读端排空。
	pr, pw, err := os.Pipe()
	if err != nil {
		return ExecResult{}, err
	}
	session.Stdout = pw
	session.Stderr = pw
	// 并发排水（issue #84 的根因）：Run 返回后才读管道时，输出超过
	// 管道缓冲（~64KB）会让 ssh 写端阻塞、Run 永不返回，只能等超时。
	// 读取与 Run 并行，且经 LimitReader 封顶防大日志撑爆内存。
	outCh := make(chan struct {
		data      []byte
		truncated bool
	}, 1)
	go func() {
		// 前 cap 字节保留；其后继续排水到 EOF——读满即停会让 ssh 写端
		// 阻塞在满管道上，Run 永不返回（大输出死锁的另一半）。
		data, _ := io.ReadAll(io.LimitReader(pr, execOutputCap))
		n, _ := io.Copy(io.Discard, pr)
		outCh <- struct {
			data      []byte
			truncated bool
		}{data, n > 0}
	}()
	done := make(chan error, 1)
	go func() { done <- session.Run("cd " + quote(c.cfg.Root) + " && " + command) }()
	select {
	case err := <-done:
		pw.Close() // 写端关闭 → 排水 goroutine 收到 EOF 收尾
		res := <-outCh
		pr.Close()
		code := 0
		if err != nil {
			code = 1
			var exitErr *ssh.ExitError
			if ee, ok := err.(*ssh.ExitError); ok {
				exitErr = ee
				code = exitErr.ExitStatus()
			}
		}
		output := string(res.data)
		if res.truncated {
			output = "…[输出超过 1MB，已截断保留头部]\n" + output
		}
		return ExecResult{Output: output, ExitCode: code}, nil
	case <-time.After(execTimeout):
		session.Close()
		// 收尾管道（issue #84：曾泄漏一对 fd）：关写端让排水收 EOF，
		// 等它退出后再关读端。
		pw.Close()
		<-outCh
		pr.Close()
		return ExecResult{}, fmt.Errorf("command timed out after %s", execTimeout)
	}
}

// tryDefaultIdentities is the default-key hook point (agent chain lands
// with the tool wiring round).
var tryDefaultIdentities = func() []interface{} { return nil }

// quote wraps a path for the remote shell (single quotes, POSIX style).
func quote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// ReadFile reads a file inside the workspace root.
func (c *Client) ReadFile(name string) ([]byte, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err // issue #220
	}
	f, err := c.sftp.Open(c.join(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// WriteFile writes a file inside the workspace root, creating parents.
func (c *Client) WriteFile(name string, data []byte, perm fs.FileMode) error {
	full := c.join(name)
	if dir := path.Dir(full); dir != "/" && dir != "" {
		if err := c.sftp.MkdirAll(dir); err != nil {
			return err
		}
	}
	f, err := c.sftp.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return c.sftp.Chmod(full, perm)
}

// Entry is one directory entry.
type Entry struct {
	Name  string
	IsDir bool
}

// ReadDir lists a directory inside the root.
func (c *Client) ReadDir(name string) ([]Entry, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err // issue #220
	}
	infos, err := c.sftp.ReadDir(c.join(name))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(infos))
	for i, info := range infos {
		out[i] = Entry{Name: info.Name(), IsDir: info.IsDir()}
	}
	return out, nil
}

// Stat reports whether a path exists and whether it is a directory.
func (c *Client) Stat(name string) (exists, isDir bool, err error) {
	if err := c.ensureConnected(); err != nil {
		return false, false, err // issue #220
	}
	info, err := c.sftp.Stat(c.join(name))
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, info.IsDir(), nil
}

// ReadLink 报告远程路径的符号链接目标：非链接或不存在时 isLink 为
// false（tools 层的远程 jail 复查靠它逐组件解析，issue #89）。
// 不经 Lstat 预检：pkg/sftp 的属性映射会丢 Windows junction 的
// reparse 位（本地 Lstat 呈 ModeIrregular，线上属性无此位），只能
// 直接试 Readlink、以错误区分非链接——junction 在服务端同样可解。
func (c *Client) ReadLink(name string) (target string, isLink bool, err error) {
	full := c.join(name)
	t, rerr := c.sftp.ReadLink(full)
	if rerr != nil {
		// 不存在 / 非链接 / 不可读：一律按非链接处理，包含性检查退回
		// 词法层兜底（不误拒合法路径）。
		return "", false, nil
	}
	// 绝对目标归一：指向根内的剥掉根前缀（根相对语），根外的保持
	// 绝对形态——tools 层把它当越狱信号（issue #89）。绝对形态两种：
	// POSIX 以 / 开头；Windows 宿主是盘符路径（C:\…），后者经 SFTP
	// 原样返回、不以 / 开头，须单独识别。比较先统一分隔符、再大小
	// 写不敏感前缀匹配（Windows 语义）。
	if strings.HasPrefix(t, "/") || windowsDriveRe.MatchString(t) {
		norm := strings.ReplaceAll(t, "\\", "/")
		root := strings.TrimSuffix(filepath.ToSlash(c.cfg.Root), "/")
		if root != "" && len(norm) > len(root) &&
			strings.EqualFold(norm[:len(root)], root) && norm[len(root)] == '/' {
			t = norm[len(root)+1:]
		}
	}
	return t, true, nil
}

// windowsDriveRe 匹配 Windows 盘符绝对路径（C:\ 或 C:/）。
var windowsDriveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// Glob walks the workspace root collecting paths that match a shell glob
// (the local Glob tool's semantics, over SFTP).
func (c *Client) Glob(pattern string) ([]string, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err // issue #220
	}
	matches, err := filepathGlobFS(func(dir string) ([]Entry, error) {
		return c.ReadDir(relOrEmpty(dir))
	}, pattern)
	return matches, err
}

// join resolves a workspace-relative name under the root (lexical jail;
// SFTP paths are always absolute here).
func (c *Client) join(name string) string {
	if !strings.HasPrefix(name, "/") {
		name = c.cfg.Root + "/" + name
	}
	return path.Clean(name)
}

func relOrEmpty(dir string) string {
	return strings.TrimPrefix(dir, "/")
}
