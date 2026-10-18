package remote

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStartBackgroundStreamAndKill 验证远端后台命令：启动不阻塞、输出
// 流式可读（而非攒到结束）、KillTree（关通道）让 Wait 立即返回——
// remotetest 服务器对齐真 SSH 的断开即杀语义，此处端到端覆盖。
func TestStartBackgroundStreamAndKill(t *testing.T) {
	c := dialTestServer(t)
	cmd := "echo bg-started && sleep 30"
	if runtime.GOOS == "windows" {
		cmd = "echo bg-started & ping -n 30 127.0.0.1 > nul"
	}
	p, err := c.StartBackground(cmd)
	if err != nil {
		t.Fatalf("StartBackground: %v", err)
	}
	defer p.Stdout().Close()
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 输出流式到达：读到首行 echo 的内容即成功（10s 预算）。
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := p.Stdout().Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, "bg-started") {
			t.Fatalf("streamed output = %q", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no streamed output within 10s")
	}

	// KillTree 关闭会话通道杀远端进程：Wait 立即返回（不等满 30s）。
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	if err := p.KillTree(); err != nil {
		t.Fatalf("KillTree: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after kill")
	}
}
