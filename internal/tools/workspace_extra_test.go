package tools

// 附加工作目录 jail 测试（spec 规则 128）：多根判定（词法 + symlink 复
// 查按所属根）、相对路径仍只落 Root、bash underRoot 同权。

import (
	"os"
	"path/filepath"
	"testing"
)

func extraEnv(t *testing.T) (Workspace, string, string) {
	t.Helper()
	root := t.TempDir()
	extra := t.TempDir()
	if err := os.WriteFile(filepath.Join(extra, "peer.txt"), []byte("邻仓内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Workspace{Root: root, Extra: []string{extra}}, root, extra
}

func TestWorkspaceExtraResolve(t *testing.T) {
	ws, root, extra := extraEnv(t)
	// 附加根内的绝对路径可读。
	abs, err := ws.Resolve(filepath.Join(extra, "peer.txt"))
	if err != nil {
		t.Fatalf("extra resolve: %v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil || string(data) != "邻仓内容" {
		t.Fatalf("read: %q %v", data, err)
	}
	// 相对路径仍只落 Root（Extra 不参与相对解析）。
	if got, err := ws.Resolve("peer.txt"); err != nil || got != filepath.Join(root, "peer.txt") {
		t.Fatalf("relative resolve = %q %v（应落 Root）", got, err)
	}
	// 两个根之外的路径拒绝。
	outside := filepath.Join(filepath.Dir(root), "elsewhere.txt")
	if _, err := ws.Resolve(outside); err == nil {
		t.Fatal("outside accepted")
	}
}

// TestWorkspaceExtraSymlinkEscape：指向附加根之外的符号链接仍被所属根
// 的真实路径复查拦下（多根不放宽 symlink 语义）。
func TestWorkspaceExtraSymlinkEscape(t *testing.T) {
	if runtimeIsWindowsUnsupported() {
		t.Skip("symlink needs privileges on windows")
	}
	root := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("越界"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(extra, "escape")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	ws := Workspace{Root: root, Extra: []string{extra}}
	if _, err := ws.Resolve(filepath.Join(extra, "escape", "secret.txt")); err == nil {
		t.Fatal("symlink escape through extra root accepted")
	}
}

// TestBashUnderRootExtra：cd 可以进入任一根。
func TestBashUnderRootExtra(t *testing.T) {
	ws, _, extra := extraEnv(t)
	b := NewBash(ws, NewJobs())
	if !b.underRoot(extra) {
		t.Fatal("extra root not underRoot")
	}
	if !b.underRoot(filepath.Join(extra, "sub")) {
		t.Fatal("extra subdir not underRoot")
	}
	if b.underRoot(filepath.Dir(extra)) {
		t.Fatal("parent of extra accepted")
	}
}

func runtimeIsWindowsUnsupported() bool {
	return os.PathSeparator == '\\' // 无特权 Windows 建 symlink 常失败——跳过而非假绿
}
