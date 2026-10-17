package remote

import (
	"strings"
	"testing"
)

// TestExecOutputCapped 验证远程命令输出尊重 1MB 上限并标注截断
// （issue #84：io.ReadAll 无上限，远程 cat 大日志全进内存）。
func TestExecOutputCapped(t *testing.T) {
	client := dialTestServer(t)
	// 大输出经文件而非 shell 管道构造（嵌套引号在 Windows 测试服务器
	// 上不可靠）：写 3MB 文件后整只 cat/type 出来。
	big := strings.Repeat("a", 3<<20)
	if err := client.WriteFile("big.log", []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := client.Exec("cat big.log")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(res.Output) > execOutputCap+64 {
		t.Fatalf("output = %d bytes, want capped", len(res.Output))
	}
	if !strings.Contains(res.Output, "已截断") {
		t.Fatal("truncation marker missing")
	}
	// 上限后连接仍可用。
	if res, err := client.Exec("echo ok"); err != nil || !strings.Contains(res.Output, "ok") {
		t.Fatalf("after cap: %+v %v", res, err)
	}
}
