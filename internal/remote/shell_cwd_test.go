package remote

import (
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// TestShellStartsInRoot 验证远程 shell 落在工作区根而非登录 home
// （issue #125：注释称 in the client's root，实现起在 ~——与本地终端
// 行为和"终端 · <工作区>"标题不符）。经 pwd 探针断言 cwd 是服务器根。
func TestShellStartsInRoot(t *testing.T) {
	srv := remotetest.Start(t)
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

	// pwd 打上标记后输出：cwd 应为服务器根（slashes 归一比较）。
	if _, err := conn.Write([]byte("pwd; echo PROBE-DONE\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = readUntil(t, conn, "PROBE-DONE", 5*time.Second)
	// 真 SSH 服务器的 shell 落在登录 home；客户端必须注入 cd（测试服
	// 务器把 cwd 设为 root，掩盖行为差异，故断言首行输入而非 pwd）。
	first := srv.FirstShellLine()
	if !strings.HasPrefix(strings.ReplaceAll(first, "\\", "/"),
		"cd ") ||
		!strings.Contains(strings.ReplaceAll(first, "\\", "/"),
			strings.ReplaceAll(srv.Root, "\\", "/")) {
		t.Fatalf("first shell input %q is not a cd into %q", first, srv.Root)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
