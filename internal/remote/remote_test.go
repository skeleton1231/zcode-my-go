package remote

import (
	"os"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
	"path/filepath"
	"strings"
	"testing"
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

// TestParseTargetDriveRoot 验证 Windows 盘符根的还原：URL 解析出的
// "/C:/…" 必须剥掉前导斜杠（服务层 ssh:// 目标即经此路；"C:/…" 才是
// 任何一侧 fs 都认得的形态）。
func TestParseTargetDriveRoot(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ssh://u@h:22/C:/Users/x/ws", "C:/Users/x/ws"},
		{"ssh://u@h/C:/ws", "C:/ws"},
		{"ssh://u@h/D:/a b/c", "D:/a b/c"},
		{"ssh://u@h//home/u/ws", "/home/u/ws"}, // POSIX 根不受影响
		{"ssh://u@h/CD/not-drive", "/CD/not-drive"}, // 第三字节非冒号
	}
	for _, c := range cases {
		cfg, err := ParseTarget(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if cfg.Root != c.want {
			t.Errorf("%s: root = %q, want %q", c.in, cfg.Root, c.want)
		}
	}
}
