package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeDirLink 在 dir 下创建指向 target 的目录链接：优先 os.Symlink，
// Windows 上失败则退回 junction（mklink /J，无需特权）。两者都不可用时
// 跳过测试（issue #4 的逃逸向量依赖它）。
func makeDirLink(t *testing.T, target, link string) {
	t.Helper()
	symErr := os.Symlink(target, link)
	if symErr == nil {
		return
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err == nil {
			return
		} else {
			t.Skipf("cannot create link (%v): %s", err, out)
		}
	}
	t.Skipf("os.Symlink unavailable: %v", symErr)
}

// jailProbe 在 workspace 外放一个秘密文件，workspace 内放指向外部的
// 目录链接，返回链接内相对路径。
func jailProbe(t *testing.T) (w Workspace, leakRel string) {
	t.Helper()
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	inside := filepath.Join(base, "inside")
	for _, d := range []string{outside, inside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET-DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, outside, filepath.Join(inside, "leakdir"))
	return Workspace{Root: inside}, filepath.Join("leakdir", "secret.txt")
}

// TestWorkspaceSymlinkEscapeRead 链接指向 jail 外：读路径必须被拒绝，
// 而不是返回 jail 外的真实路径（issue #4 实测 read 泄漏）。
func TestWorkspaceSymlinkEscapeRead(t *testing.T) {
	w, leakRel := jailProbe(t)
	if _, err := w.Resolve(leakRel); err == nil {
		t.Fatal("read through escaping link allowed")
	}
}

// TestWorkspaceSymlinkEscapeWrite 链接指向 jail 外、目标文件尚不存在：
// write 路径也必须被拒绝（解析最深已存在祖先后再检查）。
func TestWorkspaceSymlinkEscapeWrite(t *testing.T) {
	w, leakRel := jailProbe(t)
	rel := filepath.Join(filepath.Dir(leakRel), "written-by-probe.txt")
	resolved, err := w.Resolve(rel)
	if err == nil {
		t.Fatalf("write through escaping link allowed: %s", resolved)
	}
	if err := os.WriteFile(resolved, nil, 0o644); err == nil || !os.IsNotExist(err) {
		t.Fatalf("resolved path must not be writable, got err=%v", err)
	}
}

// TestWorkspaceInternalSymlinkAllowed 指向 jail 内目录的链接是合法的
// （monorepo 场景），不能因修复而误伤。
func TestWorkspaceInternalSymlinkAllowed(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, filepath.Join(base, "sub"), filepath.Join(base, "alias"))
	w := Workspace{Root: base}
	p, err := w.Resolve(filepath.Join("alias", "file.txt"))
	if err != nil {
		t.Fatalf("internal link rejected: %v", err)
	}
	if !strings.HasPrefix(p, base) {
		t.Fatalf("resolved %q outside root %q", p, base)
	}
}

// TestWorkspaceRootCaseInsensitive Windows 根路径大小写与真实盘符不一致
// （EvalSymlinks 返回实际大小写）时不能误拒。
func TestWorkspaceRootCaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only")
	}
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := Workspace{Root: strings.ToLower(base)}
	if _, err := w.Resolve("f.txt"); err != nil {
		t.Fatalf("lowercased root rejected: %v", err)
	}
}
