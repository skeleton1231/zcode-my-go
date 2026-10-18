package lsp

// 真 gopls 的 daemon 行为测试（本机有 gopls 才跑；CI 不预装——skip 是
// 设计内路径，行为等价面由 e2e 的 CLI 路径与假服务器测试守住）。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func goplsHere(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
}

// miniMod 建迷你模块，返回符号位置（Call 函数名）。
func miniMod(t *testing.T) (root, file string, line, col int) {
	t.Helper()
	root = t.TempDir()
	write(t, root, "go.mod", "module mini\n\ngo 1.21\n")
	file = "a.go"
	write(t, root, file, "package mini\n\n// Call 在注释里（references 不应命中）\nfunc Call() int { return 42 }\n\n// 另一处同名文本 \"Call\" 在字符串里\nvar s = \"Call\"\n")
	write(t, root, "b.go", "package mini\n\nfunc Use() int { return Call() }\n")
	// Call 定义行：第 4 行，列 6（func Call → 标识符在第 6 列）。
	return root, file, 4, 6
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDaemonDefinitionAgainstCLI：daemon 与 v1 CLI 对同一位置给出同源
// 定义点（file:line 前缀一致），且两次查询复用同一 daemon 进程。
func TestDaemonDefinitionAgainstCLI(t *testing.T) {
	goplsHere(t)
	root, file, line, col := miniMod(t)
	m := NewManager()
	defer m.CloseAll()
	ctx := context.Background()

	dtext, err := daemonRunWith(ctx, m, root, QueryTimeout, []string{"definition", posArg(file, line, col)})
	if err != nil {
		t.Fatalf("daemon definition: %v", err)
	}
	ctext, err := Run(ctx, root, QueryTimeout, "definition", posArg(file, line, col))
	if err != nil {
		t.Fatalf("cli definition: %v", err)
	}
	if dtext == "" {
		t.Fatal("daemon definition empty")
	}
	// 两路径都指向 a.go 的定义行（daemon 渲染相对路径、CLI 绝对路径
	// ——列口径一致，比对 basename:line:col 片段）。
	if !strings.Contains(dtext, "a.go:4:6") || !strings.Contains(ctext, "a.go:4:6") {
		t.Fatalf("daemon=%q cli=%q", dtext, ctext)
	}
	// 复用：同一根两次查询只有一个 daemon。
	m.mu.Lock()
	n := len(m.daemons)
	m.mu.Unlock()
	if n != 1 {
		t.Fatalf("daemons = %d (want 1 — daemon must be reused)", n)
	}
}

// TestDaemonReferencesSymbolic：references 走符号语义——b.go 的调用点
// 命中，注释与字符串里的同名文本不出现。
func TestDaemonReferencesSymbolic(t *testing.T) {
	goplsHere(t)
	root, file, line, col := miniMod(t)
	m := NewManager()
	defer m.CloseAll()
	text, err := daemonRunWith(context.Background(), m, root, QueryTimeout, []string{"references", posArg(file, line, col)})
	if err != nil {
		t.Fatalf("daemon references: %v", err)
	}
	if !strings.Contains(text, "b.go:") {
		t.Fatalf("call site in b.go missing: %q", text)
	}
	if strings.Count(text, "\n")+1 > 3 {
		t.Fatalf("too many hits (comment/string leaked?): %q", text)
	}
}

// TestDaemonDiagnosticsAfterEdit：坏文件出诊断；磁盘修复 + NotifyChanged
// 后诊断转空（didChangeWatchedFiles 显式失效生效——不依赖文件监视器）。
func TestDaemonDiagnosticsAfterEdit(t *testing.T) {
	goplsHere(t)
	root := t.TempDir()
	write(t, root, "go.mod", "module mini\n\ngo 1.21\n")
	write(t, root, "bad.go", "package mini\n\nfunc B() { undefinedCall() }\n")
	m := NewManager()
	defer m.CloseAll()
	ctx := context.Background()

	text, err := daemonRunWith(ctx, m, root, QueryTimeout, []string{"check", "bad.go"})
	if err != nil {
		t.Fatalf("check bad: %v", err)
	}
	if !strings.Contains(text, "undefinedCall") {
		t.Fatalf("diagnostics missing undefinedCall: %q", text)
	}
	// 修复磁盘文件并显式通知。
	write(t, root, "bad.go", "package mini\n\nfunc B() int { return 1 }\n")
	m.NotifyChanged(root, filepath.Join(root, "bad.go"))
	clean := false
	for i := 0; i < 10; i++ { // 类型检查是异步的——轮询窗口 10×500ms
		time.Sleep(500 * time.Millisecond)
		text, err = daemonRunWith(ctx, m, root, QueryTimeout, []string{"check", "bad.go"})
		if err == nil && strings.TrimSpace(text) == "" {
			clean = true
			break
		}
	}
	if !clean {
		t.Fatalf("diagnostics stale after fix+notify: %q (err=%v)", text, err)
	}
}

// TestDaemonWorkspaceCheckFallsBackToCLI：check ./... 无 LSP 等价——
// 退 CLI 路径仍可用。
func TestDaemonWorkspaceCheckFallsBackToCLI(t *testing.T) {
	goplsHere(t)
	root, _, _, _ := miniMod(t)
	m := NewManager()
	defer m.CloseAll()
	text, err := daemonRunWith(context.Background(), m, root, QueryTimeout, []string{"check", "./..."})
	if err != nil {
		t.Fatalf("workspace check: %v", err)
	}
	// mini 模块无错——CLI 对健康包输出空（或不含错误字样）。
	if strings.Contains(text, "error") && !strings.Contains(text, "0 errors") {
		t.Fatalf("workspace check reported errors: %q", text)
	}
}
