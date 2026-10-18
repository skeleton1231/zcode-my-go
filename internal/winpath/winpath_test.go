//go:build windows

package winpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// shortOf 用 GetShortPathName 造出 8.3 短形态（RUNNER~1 一类）；卷上
// 禁用 8dot3 时返回原样（测试跳过——CI runner 的 TMP 天然带短名组件，
// 长名展开在那里必被走到）。
func shortOf(t *testing.T, p string) (string, bool) {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	n, err := windows.GetShortPathName(p16, nil, 0)
	if err != nil || n == 0 {
		t.Skip("GetShortPathName sizing failed")
	}
	buf := make([]uint16, n)
	if _, err := windows.GetShortPathName(p16, &buf[0], uint32(len(buf))); err != nil {
		t.Skip("GetShortPathName failed")
	}
	short := windows.UTF16ToString(buf)
	if strings.EqualFold(short, p) {
		return "", false // 无短形态可用（8dot3 关闭）
	}
	return short, true
}

// TestLongExpandsShortName: 短名路径必须还原成长形态——git（toplevel、
// status）返回长形态而 Go 的 TempDir 沿用 TMP 的短名，字符串比较会错判
// "不是同一目录"（CI windows runner：检查点与层级上下文全军覆没的根因）。
func TestLongExpandsShortName(t *testing.T) {
	// 长名目录：组件 >8 字符才有短形态。
	dir := filepath.Join(t.TempDir(), "long-component-name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	short, ok := shortOf(t, dir)
	if !ok {
		t.Skip("volume has 8dot3 disabled; runner path exercises this in CI")
	}
	if strings.EqualFold(short, dir) {
		t.Fatal("short form equals long form?")
	}
	got := Long(short)
	// 期望值同样过 Long：同一目录的两种拼法必须归一到同一形态（dir 自
	// 身在 runner 上也带短名组件，比对原始串会把正确的展开误判成错）。
	if want := Long(dir); !strings.EqualFold(got, want) {
		t.Fatalf("Long(%q) = %q, want %q", short, got, want)
	}
}

// TestLongPassthrough: 不存在/空路径原样返回（不因坏输入失败）。
func TestLongPassthrough(t *testing.T) {
	for _, p := range []string{"", `Z:\no\such\dir\probably`, `relative\path`} {
		if got := Long(p); got != p {
			t.Fatalf("Long(%q) = %q, want identity", p, got)
		}
	}
}
