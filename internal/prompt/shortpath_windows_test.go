//go:build windows

package prompt

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// shortPathOf returns the 8.3 short form of dir（不可用时 ok=false——
// 卷禁用 8dot3 时跳过依赖它的测试；CI runner 的 TMP 天然带短名组件）。
func shortPathOf(t *testing.T, dir string) (string, bool) {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := windows.GetShortPathName(p16, nil, 0)
	if err != nil || n == 0 {
		return "", false
	}
	buf := make([]uint16, n)
	if _, err := windows.GetShortPathName(p16, &buf[0], uint32(len(buf))); err != nil {
		return "", false
	}
	short := windows.UTF16ToString(buf)
	if strings.EqualFold(short, dir) {
		return "", false
	}
	return short, true
}
