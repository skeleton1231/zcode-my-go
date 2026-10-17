package service

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// TestTerminalConnSSHAndLocal: ssh sessions hand out a live remote shell;
// local sessions return (nil, nil) for the caller's local branch.
func TestTerminalConnSSHAndLocal(t *testing.T) {
	srv := remotetest.Start(t)
	if err := os.WriteFile(filepath.Join(srv.Root, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZGO_SSH_KEY", srv.KeyPEM) // openWorkspace 从环境取私钥
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)

	ssh, err := svc.NewSession("ssh://t@127.0.0.1" + portOf(t, srv.Address) + "/" + filepath.ToSlash(srv.Root))
	if err != nil {
		t.Skipf("ssh session: %v", err)
	}
	conn, err := svc.TerminalConn(ssh.ID)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("echo svc-shell-ok\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := drainUntil(t, conn, "svc-shell-ok", 8*time.Second); !strings.Contains(got, "svc-shell-ok") {
		t.Fatalf("output = %q", got)
	}

	// 本地会话：nil, nil。
	local, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c2, err := svc.TerminalConn(local.ID)
	if err != nil || c2 != nil {
		t.Fatalf("local conn = %v, %v", c2, err)
	}
}

func portOf(t *testing.T, addr string) string {
	t.Helper()
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		t.Fatalf("bad address %q", addr)
	}
	return addr[i:]
}

func drainUntil(t *testing.T, r io.Reader, marker string, timeout time.Duration) string {
	t.Helper()
	type chunk struct {
		data string
	}
	ch := make(chan chunk, 1)
	go func() {
		var acc strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if strings.Contains(acc.String(), marker) {
					break
				}
			}
			if err != nil {
				break
			}
		}
		ch <- chunk{acc.String()}
	}()
	select {
	case c := <-ch:
		return c.data
	case <-time.After(timeout):
		return ""
	}
}
