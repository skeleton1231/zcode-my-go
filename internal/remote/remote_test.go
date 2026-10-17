package remote

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// dialTestServer dials the in-process server for real round-trips.
func dialTestServer(t *testing.T) *Client {
	t.Helper()
	srv := remotetest.Start(t)
	host, port, _ := splitHostPort(srv.Address)
	client, err := Dial(Config{
		User: "tester", Host: host, Port: port,
		Key: srv.KeyPEM, Root: srv.Root,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in             string
		user, host, pt string
		root           string
	}{
		{"ssh://tom@box.net:2222/srv/app", "tom", "box.net", "2222", "/srv/app"},
		{"ssh://box.net/workspace", "", "box.net", "", "/workspace"},
		{"ssh://git@10.0.0.1", "git", "10.0.0.1", "", "/"},
	}
	for _, c := range cases {
		got, err := ParseTarget(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got.User != c.user || got.Host != c.host || got.Port != c.pt || got.Root != c.root {
			t.Errorf("%s = %+v", c.in, got)
		}
	}
	if _, err := ParseTarget(`D:\local\path`); err == nil {
		t.Fatal("local path parsed as ssh")
	}
	if _, err := ParseTarget("ssh:///nopath"); err == nil {
		t.Fatal("hostless target parsed")
	}
}

func TestExecRoundTrip(t *testing.T) {
	client := dialTestServer(t)
	res, err := client.Exec("echo hello-ssh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "hello-ssh") {
		t.Fatalf("output = %q", res.Output)
	}
	res, err = client.Exec("exit 7")
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
}

func TestFileRoundTrip(t *testing.T) {
	client := dialTestServer(t)

	if err := client.WriteFile("sub/a.txt", []byte("内容"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := client.ReadFile("sub/a.txt")
	if err != nil || string(data) != "内容" {
		t.Fatalf("read = %q %v", data, err)
	}
	exists, isDir, err := client.Stat("sub")
	if err != nil || !exists || !isDir {
		t.Fatalf("stat sub = %v %v %v", exists, isDir, err)
	}
	exists, _, _ = client.Stat("nope.txt")
	if exists {
		t.Fatal("missing file reports present")
	}
	entries, err := client.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "sub" && e.IsDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestGlobRemote(t *testing.T) {
	client := dialTestServer(t)
	client.WriteFile("a/x.go", []byte("1"), 0o644)
	client.WriteFile("a/b/y.go", []byte("2"), 0o644)
	client.WriteFile("a/top.txt", []byte("3"), 0o644)

	got, err := client.Glob("**/*.go")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"a/x.go", "a/b/y.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("glob missing %q: %v", want, got)
		}
	}
	if strings.Contains(joined, "top.txt") {
		t.Error("glob matched non-go file")
	}
}

// splitHostPort splits host:port without net import cycles in helpers.
func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

// 磁盘根对齐检查：WriteFile 的目标应落在真实根内（测试服务器直接服务
// 本地文件系统，root 即 t.TempDir）。
func TestWriteLandsInRoot(t *testing.T) {
	srv := remotetest.Start(t)
	host, port, _ := splitHostPort(srv.Address)
	client, err := Dial(Config{User: "t", Host: host, Port: port, Key: srv.KeyPEM, Root: srv.Root})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.WriteFile("probe.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv.Root, "probe.txt")); err != nil {
		t.Fatalf("file not in root: %v", err)
	}
}

func countOpenFDs() int {
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		entries, err := os.ReadDir(dir)
		if err == nil {
			return len(entries)
		}
	}
	return -1
}

func TestExecTimeoutCleanupAndReuse(t *testing.T) {
	client := dialTestServer(t)

	oldTimeout := execTimeout
	execTimeout = 100 * time.Millisecond
	t.Cleanup(func() { execTimeout = oldTimeout })

	initialFDs := countOpenFDs()

	// Command sleeps longer than execTimeout to trigger the timeout branch.
	_, err := client.Exec("sleep 2")
	if err == nil {
		t.Fatal("expected exec to timeout, got nil error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("expected timeout error message, got %v", err)
	}

	// Restore normal timeout before subsequent reuse command so it does not time out under load.
	execTimeout = oldTimeout

	// Verify the client remains usable and can execute subsequent commands.
	res, err := client.Exec("echo reuse-ok")
	if err != nil {
		t.Fatalf("exec after timeout failed: %v", err)
	}
	if !strings.Contains(res.Output, "reuse-ok") {
		t.Fatalf("expected reuse-ok in output, got %q", res.Output)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	// Verify file descriptors were not leaked by the timeout path.
	if initialFDs > 0 {
		currentFDs := countOpenFDs()
		if currentFDs > initialFDs+2 {
			t.Fatalf("fd leak detected: initial %d, current %d", initialFDs, currentFDs)
		}
	}
}

func TestExecOutputCap(t *testing.T) {
	client := dialTestServer(t)

	// Small output below execCapBytes should not be truncated.
	smallRes, err := client.Exec("echo short-output")
	if err != nil {
		t.Fatalf("small exec failed: %v", err)
	}
	if strings.Contains(smallRes.Output, "output truncated") {
		t.Fatalf("small output should not be marked truncated: %q", smallRes.Output)
	}
	if !strings.Contains(smallRes.Output, "short-output") {
		t.Fatalf("expected short-output, got %q", smallRes.Output)
	}

	// Large output exceeding execCapBytes (30KB) must be bounded and annotated.
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "powershell -Command \"[Console]::Write('A' * 40000)\""
	} else {
		cmd = "head -c 40000 /dev/zero | tr '\\0' 'A'"
	}

	largeRes, err := client.Exec(cmd)
	if err != nil {
		t.Fatalf("large exec failed: %v", err)
	}
	const marker = "\n… output truncated …\n"
	if !strings.HasSuffix(largeRes.Output, marker) {
		t.Fatalf("expected output to end with truncation marker, got %q", largeRes.Output)
	}
	raw := strings.TrimSuffix(largeRes.Output, marker)
	if len([]byte(raw)) != execCapBytes {
		t.Fatalf("expected raw output length %d bytes, got %d", execCapBytes, len([]byte(raw)))
	}
	if !strings.HasPrefix(raw, "AAAA") {
		t.Fatalf("expected prefix 'AAAA', got %q", raw[:min(10, len(raw))])
	}
}

func TestExecLargeOutputDoesNotDeadlock(t *testing.T) {
	client := dialTestServer(t)

	// 128KB output exceeds standard OS pipe capacity (64KB), proving the pipe is drained
	// concurrently without deadlocking the command runner.
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "powershell -Command \"[Console]::Write('B' * 131072)\""
	} else {
		cmd = "head -c 131072 /dev/zero | tr '\\0' 'B'"
	}

	res, err := client.Exec(cmd)
	if err != nil {
		t.Fatalf("large pipe exec failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	const marker = "\n… output truncated …\n"
	if !strings.HasSuffix(res.Output, marker) {
		t.Fatalf("expected output to end with truncation marker, got %q", res.Output)
	}
	raw := strings.TrimSuffix(res.Output, marker)
	if len([]byte(raw)) != execCapBytes {
		t.Fatalf("expected output capped at %d bytes, got %d", execCapBytes, len([]byte(raw)))
	}
}

