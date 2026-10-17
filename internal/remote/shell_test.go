package remote

import (
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// TestShellRoundTrip: an interactive shell over the in-process server —
// typed lines reach the remote shell, its output streams back, Resize is
// accepted, Close is idempotent.
func TestShellRoundTrip(t *testing.T) {
	srv := remotetest.Start(t) // 自带 t.Cleanup
	host, port, _ := splitHostPort(srv.Address)
	client, err := Dial(Config{User: "t", Host: host, Port: port, Key: srv.KeyPEM, Root: srv.Root})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	conn, err := client.Shell()
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("echo zgo-shell-smoke\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readUntil(t, conn, "zgo-shell-smoke", 8*time.Second)
	if !strings.Contains(got, "zgo-shell-smoke") {
		t.Fatalf("output = %q", got)
	}

	if err := conn.Resize(120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}
}

// readUntil reads in the background until the marker shows or the
// deadline passes（os.Pipe 无读超时，靠 goroutine + select 兜底）.
func readUntil(t *testing.T, conn *ShellConn, marker string, timeout time.Duration) string {
	t.Helper()
	type chunk struct {
		data string
		err  error
	}
	ch := make(chan chunk, 1)
	go func() {
		var acc strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if strings.Contains(acc.String(), marker) {
					ch <- chunk{data: acc.String()}
					return
				}
			}
			if err != nil {
				ch <- chunk{data: acc.String(), err: err}
				return
			}
		}
	}()
	select {
	case c := <-ch:
		return c.data
	case <-time.After(timeout):
		return ""
	}
}
