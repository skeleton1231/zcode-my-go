// Package clipimg reads an image off the system clipboard (issue #216：
// 截图 → Ctrl+V → 附件，桌面 agent 的肌肉记忆级入口)。Windows 优先
// 取 PNG 注册格式的原始字节；否则解 CF_DIBV5/CF_DIB（BITMAPINFOHEADER
// 族）为 image 再 PNG 化。其他平台返回 ErrNoImage（放行为普通文本粘
// 贴），解码部分是纯函数、可全平台测试。
package clipimg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// DIB 头部长度（BITMAPINFOHEADER=40；BITMAPV5HEADER=124，前者是后者
// 前缀，按 size 字段识别）。
const (
	infoHeaderSize = 40
	v5HeaderSize   = 124
)

// decodeDIB 把剪贴板 DIB（BITMAPINFOHEADER 或 BITMAPV5HEADER + 调色板
// + 像素）解为 image。支持 BI_RGB 的 32bpp（BGRA，截图工具的主形态）、
// 24bpp（BGR）与 8bpp（调色板索引）；行按 4 字节对齐补齐；正高为
// bottom-up、负高为 top-down。BI_BITFIELDS 罩码按 32bpp 常见罩码
// （红绿蓝任一序）兼容。其余压缩/位深返回错误（调用方放行文本粘贴，
// 不是崩溃路径）。
func decodeDIB(data []byte) (image.Image, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("dib too short: %d", len(data))
	}
	size := int(binary.LittleEndian.Uint32(data[0:4]))
	if size != infoHeaderSize && size != v5HeaderSize && size != 108 && size != 56 && size != 52 {
		return nil, fmt.Errorf("unsupported dib header size %d", size)
	}
	if len(data) < size {
		return nil, fmt.Errorf("dib truncated: header %d of %d", size, len(data))
	}
	width := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	height := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	planes := int(binary.LittleEndian.Uint16(data[12:14]))
	bitCount := int(binary.LittleEndian.Uint16(data[14:16]))
	compression := int(binary.LittleEndian.Uint32(data[16:20]))
	clrUsed := int(binary.LittleEndian.Uint32(data[32:36]))
	if planes != 1 {
		return nil, fmt.Errorf("dib planes %d", planes)
	}
	topDown := false
	if height < 0 {
		topDown = true
		height = -height
	}
	if width <= 0 || height <= 0 || height > 1<<16 || width > 1<<16 {
		return nil, fmt.Errorf("dib size %dx%d out of range", width, height)
	}

	var palette []color.RGBA
	switch compression {
	case 0: // BI_RGB
	case 3: // BI_BITFIELDS：罩码在调色板位（仅 32/16bpp 常见）；截图工
		// 具的 32bpp 罩码=不压缩的位序，按 32bpp 处理。
		if bitCount != 32 {
			return nil, fmt.Errorf("dib BI_BITFIELDS at %dbpp unsupported", bitCount)
		}
	default:
		return nil, fmt.Errorf("dib compression %d unsupported", compression)
	}
	if clrUsed > 0 || bitCount == 8 {
		n := clrUsed
		if n == 0 && bitCount == 8 {
			n = 256
		}
		palOff := size
		if compression == 3 {
			palOff = size + 12 // 三枚 DWORD 罩码之后才是调色板
		}
		if palOff+n*4 > len(data) {
			return nil, fmt.Errorf("dib palette truncated")
		}
		for i := 0; i < n; i++ {
			b := data[palOff+i*4 : palOff+i*4+4]
			palette = append(palette, color.RGBA{R: b[2], G: b[1], B: b[0], A: 0xFF})
		}
	}
	pixOff := size
	if len(palette) > 0 {
		if compression == 3 {
			pixOff = size + 12 + len(palette)*4
		} else {
			pixOff = size + len(palette)*4
		}
	}
	if bitCount == 8 && len(palette) == 0 {
		return nil, fmt.Errorf("dib 8bpp without palette")
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rowBytes := (width*bitCount + 31) / 32 * 4
	if pixOff+rowBytes*height > len(data) {
		return nil, fmt.Errorf("dib pixels truncated: need %d have %d",
			pixOff+rowBytes*height, len(data))
	}
	for y := 0; y < height; y++ {
		src := data[pixOff+y*rowBytes : pixOff+(y+1)*rowBytes]
		dstY := y
		if !topDown {
			dstY = height - 1 - y
		}
		row := img.Pix[dstY*img.Stride : dstY*img.Stride+width*4]
		switch bitCount {
		case 32:
			for x := 0; x < width; x++ {
				row[x*4+0] = src[x*4+2]
				row[x*4+1] = src[x*4+1]
				row[x*4+2] = src[x*4+0]
				row[x*4+3] = 0xFF
			}
		case 24:
			for x := 0; x < width; x++ {
				row[x*4+0] = src[x*3+2]
				row[x*4+1] = src[x*3+1]
				row[x*4+2] = src[x*3+0]
				row[x*4+3] = 0xFF
			}
		case 8:
			for x := 0; x < width; x++ {
				idx := int(src[x])
				if idx >= len(palette) {
					idx = 0
				}
				p := palette[idx]
				row[x*4+0], row[x*4+1], row[x*4+2], row[x*4+3] = p.R, p.G, p.B, p.A
			}
		default:
			return nil, fmt.Errorf("dib %dbpp unsupported", bitCount)
		}
	}
	return img, nil
}

// dibToPNG 解 DIB 并编码 PNG（纯函数、跨平台——Windows 读取面与测试
// 共用）。
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

// pngMagic 是 PNG 文件头（跨平台：测试与 Windows 读取面共用；防御注
// 册格式数据偶见带包装）。
var pngMagic = []byte{0x89, 'P', 'N', 'G'}
