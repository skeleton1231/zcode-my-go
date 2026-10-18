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

// goplsLikeFake 在 PATH 放一个假 gopls：version 输出标记、slow 睡眠。
// Windows 写 .cmd 批处理（一等平台，本机即测）；类 Unix 写 /bin/sh。
func goplsLikeFake(t *testing.T) bool {
	t.Helper()
	bin := t.TempDir()
	if isWindows() {
		if err := os.WriteFile(filepath.Join(bin, "gopls.cmd"), []byte(
			"@echo off\r\n"+
				"if \"%1\"==\"version\" echo fake-gopls-1.0\r\n"+
				"if \"%1\"==\"slow\" ping -n 6 127.0.0.1 >nul\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		return true
	}
	p := filepath.Join(bin, "gopls")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  version) echo fake-gopls-1.0 ;;\n" +
		"  slow) sleep 5 ;;\n" +
		"esac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return true
}
