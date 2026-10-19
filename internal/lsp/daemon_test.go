package lsp

// daemon 生命周期测试：不依赖真 gopls——崩溃重启与 CLI 退化用注入
//（goplsPath 指向不存在的命令模拟未装）。

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDaemonFallbackWhenMissing：gopls 不在（模拟）时 DaemonRun 退回
// v1 CLI 路径——本机装了 gopls，CLI 路径照常出结果（可用性优先）。
func TestDaemonFallbackWhenMissing(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH — CLI fallback has nothing to serve")
	}
	old := goplsPath
	goplsPath = "zgo-definitely-missing-gopls"
	t.Cleanup(func() { goplsPath = old })

	// 用 definition 形态断言退化后仍有结果（check ./... 在极简模块上
	// 有 gopls CLI 平台怪癖，不适合当退化探针）。
	root, file, line, col := miniMod(t)
	m := NewManager()
	defer m.CloseAll()
	arg := posArg(file, line, col)
	text, err := daemonRunWith(context.Background(), m, root, QueryTimeout, []string{"definition", arg})
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	cliText, err := Run(context.Background(), root, QueryTimeout, "definition", arg)
	if err != nil {
		t.Fatalf("cli: %v", err)
	}
	if text == "" || cliText == "" || !strings.Contains(text, "a.go:4:6") || !strings.Contains(cliText, "a.go:4:6") {
		t.Fatalf("fallback text=%q cli=%q", text, cliText)
	}
	// daemon 确实没起来（goplsPath 不存在）。
	m.mu.Lock()
	n := len(m.daemons)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("daemons = %d (want 0 when gopls missing)", n)
	}
}

// TestDaemonRestartAfterCrash：进程被杀后下一次调用透明重启。
func TestDaemonRestartAfterCrash(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
	root, file, line, col := miniMod(t)
	m := NewManager()
	defer m.CloseAll()

	// 第一次查询（daemon 路由形态）拉起 daemon。
	if _, err := daemonRunWith(context.Background(), m, root, QueryTimeout, []string{"definition", posArg(file, line, col)}); err != nil {
		t.Fatalf("first: %v", err)
	}
	m.mu.Lock()
	var d *daemon
	for _, v := range m.daemons {
		d = v
	}
	m.mu.Unlock()
	if d == nil {
		t.Fatal("no daemon started")
	}
	d.mu.Lock()
	pid := d.cmd.Process.Pid
	d.mu.Unlock()
	// 外部杀掉——模拟 gopls 崩溃。
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // 读循环感知 EOF
	// 下一次调用应重启新进程并成功（同一定义查询）。
	text, err := daemonRunWith(context.Background(), m, root, QueryTimeout, []string{"definition", posArg(file, line, col)})
	if err != nil {
		t.Fatalf("after crash: %v", err)
	}
	if text == "" {
		t.Fatal("definition empty after restart")
	}
	m.mu.Lock()
	var d2 *daemon
	for _, v := range m.daemons {
		d2 = v
	}
	m.mu.Unlock()
	d2.mu.Lock()
	pid2 := 0
	if d2.cmd != nil && d2.cmd.Process != nil {
		pid2 = d2.cmd.Process.Pid
	}
	d2.mu.Unlock()
	if pid2 == 0 || pid2 == pid {
		t.Fatalf("daemon not restarted: old=%d new=%d", pid, pid2)
	}
}
