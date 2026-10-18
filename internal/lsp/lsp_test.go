package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAvailableMatchesPath：PATH 上放了假 gopls 即可用（Windows 写
// .bat，Unix 写可执行脚本——CI 两平台都过）。
func TestAvailableMatchesPath(t *testing.T) {
	bin := t.TempDir()
	if isWindows() {
		if err := os.WriteFile(filepath.Join(bin, "gopls.bat"), []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		p := filepath.Join(bin, "gopls")
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if !Available() {
		t.Fatal("gopls on PATH but unavailable")
	}
	t.Setenv("PATH", "")
	if Available() {
		t.Fatal("available without gopls")
	}
}

func isWindows() bool { return strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") }

// TestRunExecutesAndCaptures：子进程输出与 stderr 混排可见。
func TestRunExecutesAndCaptures(t *testing.T) {
	if !goplsLikeFake(t) {
		t.Skip("fake shell unavailable")
	}
	out, err := Run(context.Background(), ".", time.Second, "version")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "fake-gopls") {
		t.Fatalf("output: %q", out)
	}
}

// TestRunTimeout：超时杀进程并报可读错误。
func TestRunTimeout(t *testing.T) {
	if !goplsLikeFake(t) {
		t.Skip("fake shell unavailable")
	}
	_, err := Run(context.Background(), ".", 50*time.Millisecond, "slow")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error: %v", err)
	}
}

// TestRunEmptyCommand：空命令参数拒绝。
func TestRunEmptyCommand(t *testing.T) {
	if _, err := Run(context.Background(), ".", time.Second); err == nil {
		t.Fatal("empty args accepted")
	}
}

// TestPosArg：位置参数形态。
func TestPosArg(t *testing.T) {
	if got := posArg("a/b.go", 12, 34); got != "a/b.go:12:34" {
		t.Fatalf("posArg: %q", got)
	}
}

// goplsLikeFake 在 PATH 放一个假 gopls：version 输出标记；slow 用纯
// 内建无限循环（不依赖 ping/sleep 等外部命令——CI 曾因 PATH 解析不到
// 系统命令、脚本秒退带错，非零退出+非空输出被 Run 视为有效结果）。
// Windows 写 .cmd（一等平台），类 Unix 写 /bin/sh 脚本。PATH 前置。
func goplsLikeFake(t *testing.T) bool {
	t.Helper()
	bin := t.TempDir()
	if isWindows() {
		if err := os.WriteFile(filepath.Join(bin, "gopls.cmd"), []byte(
			"@echo off\r\n"+
				"if \"%1\"==\"version\" echo fake-gopls-1.0\r\n"+
				"if \"%1\"==\"slow\" goto loop\r\n"+
				"goto :eof\r\n"+
				":loop\r\n"+
				"goto loop\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		p := filepath.Join(bin, "gopls")
		script := "#!/bin/sh\n" +
			"case \"$1\" in\n" +
			"  version) echo fake-gopls-1.0 ;;\n" +
			"  slow) while :; do :; done ;;\n" +
			"esac\n"
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return true
}
