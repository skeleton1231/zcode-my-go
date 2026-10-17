package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestOneLineRuneSafe 验证截断按 rune 进行（issue #40：按字节截断让中文
// 路径在 [工具] 行尾必然乱码——非法 UTF-8 直出终端）。
func TestOneLineRuneSafe(t *testing.T) {
	long := strings.Repeat("项", 40)
	for _, got := range []string{
		oneLine(`{"path":"` + long + `"}`),
		firstLine("line\n" + long),
	} {
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8: %q", got)
		}
	}
	if oneLine("短") != "短" {
		t.Fatal("short string mangled")
	}
}
