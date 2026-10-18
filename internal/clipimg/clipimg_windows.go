//go:build windows

package clipimg

// Windows 剪贴板位图读取：不引 OLE（CF_DIB/CF_DIBV5 与 PNG 注册格式
// 经 user32 即可直取）。取图顺序：PNG 注册格式（截图工具常放，原始字
// 节零转换）→ CF_DIBV5 → CF_DIB（两者都解 DIB 后 PNG 化）。全程持锁
// 时间最小化：拷出字节即 CloseClipboard。

import (
	"bytes"
	"fmt"
	"image/png"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procIsFormatAvail    = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procRegisterFormat   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalSize       = kernel32.NewProc("GlobalSize")
)

// 标准剪贴板格式号（CF_DIB=8、CF_DIBV5=17；PNG 是注册格式，运行时取）。
const (
	cfDIB   = 8
	cfDIBV5 = 17
)

// pngMagic 是 PNG 文件头（防御：注册格式的数据偶见带文件名包装）。
var pngMagic = []byte{0x89, 'P', 'N', 'G'}

// ReadImage 返回剪贴板位图的 PNG 字节；无图/不可用返回 ErrNoImage
// 之外的错误仅限打开失败与解码失败——调用方一律按无图放行文本粘贴，
// 不向用户弹错（截图不在剪贴板的场景是常态而非异常）。
func ReadImage() ([]byte, error) {
	if err := openClipboard(); err != nil {
		return nil, ErrNoImage // 打不开视同无图：不放行为错误路径
	}
	defer procCloseClipboard.Call(0)

	if b, ok := clipboardFormat(pngFormatID()); ok {
		if bytes.HasPrefix(b, pngMagic) {
			return b, nil
		}
	}
	for _, format := range []uintptr{cfDIBV5, cfDIB} {
		if b, ok := clipboardFormat(format); ok {
			if png, err := dibToPNG(b); err == nil {
				return png, nil
			}
		}
	}
	return nil, ErrNoImage
}

// openClipboard 带短暂重试：剪贴板可能被其他进程瞬时持有。
func openClipboard() error {
	for i := 0; i < 10; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("clipboard busy")
}

// pngFormatID 注册并缓存 "PNG" 格式号（系统级常量，注册一次进程内不变）。
var pngFormatID = func() func() uintptr {
	var id uintptr
	return func() uintptr {
		if id == 0 {
			name, _ := windows.UTF16PtrFromString("PNG")
			r, _, _ := procRegisterFormat.Call(uintptr(unsafe.Pointer(name)))
			id = r
		}
		return id
	}
}()

// clipboardFormat 取一个格式的数据副本（GlobalLock 拷出即解锁——不在
// 锁内做解码）。格式不可用返回 ok=false。
func clipboardFormat(format uintptr) ([]byte, bool) {
	if format == 0 {
		return nil, false
	}
	avail, _, _ := procIsFormatAvail.Call(format)
	if avail == 0 {
		return nil, false
	}
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil, false
	}
	p, _, _ := syscall.SyscallN(procGlobalLock.Addr(), h)
	if p == 0 {
		return nil, false
	}
	size, _, _ := procGlobalSize.Call(h)
	out := make([]byte, size)
	// Call 返回的 uintptr 实为指针：经存储重解释取回（vet 不认可
	// uintptr→Pointer 直转——无法证明其来源；指针到指针的转换合法）。
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	copy(out, unsafe.Slice((*byte)(ptr), size))
	procGlobalUnlock.Call(h)
	return out, true
}

// dibToPNG 解 DIB 并编码 PNG。
func dibToPNG(dib []byte) ([]byte, error) {
	img, err := decodeDIB(dib)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
