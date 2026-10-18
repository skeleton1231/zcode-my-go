//go:build clipsmoke

package clipimg

// 一次性真实剪贴板冒烟（CI 不跑，本机 go test -tags clipsmoke）：测试
// 进程自己把一张 CF_DIB 放上剪贴板，再走 ReadImage 全链路（Open/
// Available/GetData/GlobalLock/DIB→PNG）读回验证。tag 门控不进常规测
// 试面（剪贴板是进程外共享资源，CI 环境不可依赖）。

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"
	"unsafe"
)

var (
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
)

func TestClipboardSmoke(t *testing.T) {
	// 1x1 32bpp 蓝色 DIB（复用 dib_test.go 的 buildDIB——同包测试共享）。
	dib := buildDIB(t, 1, 1, 32, nil, []byte{0xFF, 0x00, 0x00, 0xFF}) // BGRA=blue
	h, _, _ := procGlobalAlloc.Call(0x0002 /*GMEM_MOVEABLE*/, uintptr(len(dib)))
	if h == 0 {
		t.Skip("GlobalAlloc failed")
	}
	mem, _, _ := procGlobalLock.Call(h)
	if mem == 0 {
		t.Skip("GlobalLock failed")
	}
	// Call 返回的 uintptr 实为指针：存储重解释取回（同 clipboardFormat）。
	hdr := struct{ p uintptr; l, c int }{mem, len(dib), len(dib)}
	dst := *(*[]byte)(unsafe.Pointer(&hdr))
	copy(dst, dib)
	procGlobalUnlock.Call(h)

	if err := openClipboard(); err != nil {
		t.Skipf("open: %v", err)
	}
	procEmptyClipboard.Call()
	r, _, err := procSetClipboardData.Call(cfDIB, h)
	procCloseClipboard.Call(0)
	if r == 0 {
		t.Skipf("SetClipboardData: %v", err)
	}

	pngBytes, err := ReadImage()
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if !bytes.HasPrefix(pngBytes, pngMagic) {
		t.Fatalf("not png: % x", pngBytes[:8])
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("bounds: %v", img.Bounds())
	}
	r0, g0, b0, _ := img.At(0, 0).RGBA()
	if r0 != 0 || g0 != 0 || b0 != 0xFFFF { // RGBA() 是 16 位分量
		t.Fatalf("pixel = (%d,%d,%d), want blue", r0, g0, b0)
	}
	_ = color.RGBA{}
	t.Logf("read back %d png bytes, blue pixel verified", len(pngBytes))
}
