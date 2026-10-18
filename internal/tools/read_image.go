package tools

// read 工具的图片支持（spec 规则 47）：图片文件以 BlockImage 随工具结
// 果进入模型上下文（ZCode read-image 的落地）。口径对齐 ZCode：输入
// 20MB、维度 2000、目标二进制 3.75MB（base64 5MB 上限的 3/4）。超限的
// png/jpeg 缩放重编码（stdlib，无新依赖）；gif 取首帧；webp 无 stdlib
// 解码器——≤ 目标值原样透传，超限报错并提示转换。

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

const (
	readImageMaxInputBytes = 20 << 20 // 原始文件上限
	readImageTargetBytes   = 3 << 20  // 缩放目标（3MB——留余量到 3.75MB 硬线）
	readImageHardBytes     = 3_750_000
	readImageMaxDimension  = 2000
	readImageSupportedNote = "支持 png/jpeg/gif/webp；webp 超限时先用 bash 转成 png"
)

// imageExts 是按扩展名识别的图片形态。
var imageExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// isImagePath 报告路径扩展名是否图片。
func isImagePath(path string) (mime string, ok bool) {
	mime, ok = imageExts[strings.ToLower(filepath.Ext(path))]
	return mime, ok
}

// readImageLocal 读本地图片并产出带图块的结果。
func readImageLocal(rel, abs, mime string) agent.ToolResult {
	data, err := os.ReadFile(abs)
	if err != nil {
		return errResult("cannot read %s: %v", rel, err)
	}
	return imageResult(rel, mime, data)
}

// imageResult 把图片字节加工成工具结果：必要时缩放，base64 封顶校验。
func imageResult(rel, mime string, data []byte) agent.ToolResult {
	if len(data) > readImageMaxInputBytes {
		return errResult("%s 超过图片读取上限（%d bytes）", rel, readImageMaxInputBytes)
	}
	width, height := 0, 0
	out, outMime := data, mime
	if mime == "image/webp" {
		// webp 无 stdlib 解码器：尺寸未知、无法本地缩放——≤ 硬线上
		// 限原样透传，超限报错并给转换提示。
		if len(out) > readImageHardBytes {
			return errResult("%s（webp，%d bytes）超出可上送大小且 webp 无法本地缩放——%s", rel, len(data), readImageSupportedNote)
		}
	} else {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return errResult("%s 不是有效的 %s 图片：%v", rel, mime, err)
		}
		width, height = cfg.Width, cfg.Height
		if width > readImageMaxDimension || height > readImageMaxDimension || len(out) > readImageHardBytes {
			resized, rMime, rw, rh, err := downscaleImage(data, mime)
			if err != nil {
				return errResult("processing %s: %v", rel, err)
			}
			out, outMime, width, height = resized, rMime, rw, rh
		}
	}
	if base64.StdEncoding.EncodedLen(len(out)) > 5<<20 {
		return errResult("%s 缩放后仍超出 5MB 上限", rel)
	}
	dims := ""
	if width > 0 && height > 0 {
		dims = fmt.Sprintf("，%d×%d", width, height)
	}
	return agent.ToolResult{
		Content: fmt.Sprintf("图片 %s：%s%s，%d bytes。图片内容随本结果一并提供。",
			rel, outMime, dims, len(out)),
		Images: []ai.Block{{Kind: ai.BlockImage, Media: outMime,
			Data: base64.StdEncoding.EncodeToString(out)}},
	}
}

// downscaleImage 把图片缩到目标内（先按维度等比，再按字节二分收敛），
// 返回新字节与实际尺寸。gif 取首帧转 png；png 保持 png；jpeg 重编码。
func downscaleImage(data []byte, mime string) ([]byte, string, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := 1.0
	if m := float64(readImageMaxDimension) / float64(max(w, h)); m < scale {
		scale = m
	}
	for attempt := 0; attempt < 5; attempt++ {
		nw, nh := int(float64(w)*scale), int(float64(h)*scale)
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		resized := scaleImage(img, nw, nh)
		var buf bytes.Buffer
		outMime := mime
		switch mime {
		case "image/png", "image/gif":
			outMime = "image/png"
			err = png.Encode(&buf, resized)
		default: // jpeg
			err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85})
		}
		if err != nil {
			return nil, "", 0, 0, err
		}
		if buf.Len() <= readImageHardBytes {
			return buf.Bytes(), outMime, nw, nh, nil
		}
		scale *= 0.7 // 未达标继续缩
	}
	return nil, "", 0, 0, fmt.Errorf("cannot fit image under %d bytes", readImageHardBytes)
}

// scaleImage 双线性等比缩放（stdlib 绘制，无第三方依赖）。
func scaleImage(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	for y := 0; y < h; y++ {
		sy := (float64(y) + 0.5) * float64(b.Dy()) / float64(h)
		y0 := int(sy)
		if y0 >= b.Dy()-1 {
			y0 = b.Dy() - 2
		}
		if y0 < 0 {
			y0 = 0
		}
		fy := sy - float64(y0)
		for x := 0; x < w; x++ {
			sx := (float64(x) + 0.5) * float64(b.Dx()) / float64(w)
			x0 := int(sx)
			if x0 >= b.Dx()-1 {
				x0 = b.Dx() - 2
			}
			if x0 < 0 {
				x0 = 0
			}
			fx := sx - float64(x0)
			c00 := src.At(b.Min.X+x0, b.Min.Y+y0)
			c10 := src.At(b.Min.X+x0+1, b.Min.Y+y0)
			c01 := src.At(b.Min.X+x0, b.Min.Y+y0+1)
			c11 := src.At(b.Min.X+x0+1, b.Min.Y+y0+1)
			dst.Set(x, y, bilinear(c00, c10, c01, c11, fx, fy))
		}
	}
	return dst
}

// bilinear 四点插值（RGBA 通道线性）。
func bilinear(c00, c10, c01, c11 color.Color, fx, fy float64) color.RGBA64 {
	ch := func(v00, v10, v01, v11 uint32) uint16 {
		top := float64(v00)*(1-fx) + float64(v10)*fx
		bottom := float64(v01)*(1-fx) + float64(v11)*fx
		return uint16(top*(1-fy) + bottom*fy)
	}
	r00, g00, b00, a00 := c00.RGBA()
	r10, g10, b10, a10 := c10.RGBA()
	r01, g01, b01, a01 := c01.RGBA()
	r11, g11, b11, a11 := c11.RGBA()
	return color.RGBA64{R: ch(r00, r10, r01, r11), G: ch(g00, g10, g01, g11),
		B: ch(b00, b10, b01, b11), A: ch(a00, a10, a01, a11)}
}
