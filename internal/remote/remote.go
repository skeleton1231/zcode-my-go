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
	"strings"
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
	return &Client{cfg: cfg, conn: conn, sftp: sftpClient, closer: sftpClient}, nil
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
var execTimeout = 10 * time.Minute

// execCapBytes bounds combined stdout+stderr read into memory.
const execCapBytes = 30 << 10

// ExecResult is one command's outcome.
type ExecResult struct {
	Output   string // combined stdout+stderr, in arrival order
	ExitCode int
}

// Exec runs one command line in the workspace root.
func (c *Client) Exec(command string) (ExecResult, error) {
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

	type readResult struct {
		out []byte
		err error
	}
	readDone := make(chan readResult, 1)
	go func() {
		limited := io.LimitReader(pr, execCapBytes+1)
		out, rerr := io.ReadAll(limited)
		_, _ = io.Copy(io.Discard, pr)
		readDone <- readResult{out: out, err: rerr}
	}()

	done := make(chan error, 1)
	go func() { done <- session.Run("cd " + quote(c.cfg.Root) + " && " + command) }()
	select {
	case err := <-done:
		_ = pw.Close()
		res := <-readDone
		_ = pr.Close()
		out := res.out
		truncated := false
		if int64(len(out)) > execCapBytes {
			out = out[:execCapBytes]
			truncated = true
		}
		output := string(out)
		if truncated {
			output += "\n… output truncated …\n"
		}
		code := 0
		if err != nil {
			code = 1
			var exitErr *ssh.ExitError
			if ee, ok := err.(*ssh.ExitError); ok {
				exitErr = ee
				code = exitErr.ExitStatus()
			}
		}
		return ExecResult{Output: output, ExitCode: code}, nil
	case <-time.After(execTimeout):
		_ = session.Close()
		_ = pw.Close()
		<-readDone
		_ = pr.Close()
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
	info, err := c.sftp.Stat(c.join(name))
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, info.IsDir(), nil
}

// Glob walks the workspace root collecting paths that match a shell glob
// (the local Glob tool's semantics, over SFTP).
func (c *Client) Glob(pattern string) ([]string, error) {
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
