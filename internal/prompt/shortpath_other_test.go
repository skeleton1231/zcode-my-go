//go:build !windows

package prompt

import "testing"

func shortPathOf(t *testing.T, dir string) (string, bool) {
	t.Helper()
	return "", false // 非 Windows 无 8.3 短名
}
