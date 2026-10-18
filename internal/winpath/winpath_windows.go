//go:build windows

package winpath

import "golang.org/x/sys/windows"

func long(p string) string {
	if p == "" {
		return p
	}
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	n, err := windows.GetLongPathName(p16, nil, 0)
	if err != nil || n == 0 {
		return p // 不存在或无短形态：原样
	}
	buf := make([]uint16, n)
	if _, err := windows.GetLongPathName(p16, &buf[0], uint32(len(buf))); err != nil {
		return p
	}
	return windows.UTF16ToString(buf)
}
