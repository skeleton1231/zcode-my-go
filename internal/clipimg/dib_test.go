package clipimg

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// buildDIB 构造一个 CF_DIB 形态的测试位图：header + 可选调色板 + 像素。
func buildDIB(t *testing.T, width, height, bitCount int, palette []color.RGBA, pixels []byte) []byte {
	t.Helper()
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:4], 40)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(width))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(height))
	binary.LittleEndian.PutUint16(hdr[12:14], 1)
	binary.LittleEndian.PutUint16(hdr[14:16], uint16(bitCount))
	// compression=BI_RGB、clrUsed 由调用方经 palette 控制
	binary.LittleEndian.PutUint32(hdr[32:36], uint32(len(palette)))
	var pal []byte
	for _, c := range palette {
		pal = append(pal, c.B, c.G, c.R, 0xFF)
	}
	return append(append(hdr, pal...), pixels...)
}

func pixel(t *testing.T, img image.Image, x, y int) color.RGBA {
	t.Helper()
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
}

// TestDecodeDIB32bppBottomUp：BGRA bottom-up 是截图工具的主形态（正高
// = 行序自底向上，首行像素落在图底）。
func TestDecodeDIB32bppBottomUp(t *testing.T) {
	// 2x2：行1（数据首行）红/绿，行0 蓝/白 —— 图像里 y=0 是蓝/白。
	px := []byte{
		0x00, 0x00, 0xFF, 0xFF, 0x00, 0xFF, 0x00, 0xFF, // bottom row: red, green
		0xFF, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // top row: blue, white
	}
	img, err := decodeDIB(buildDIB(t, 2, 2, 32, nil, px))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]color.RGBA{
		{pixel(t, img, 0, 0), {0xFF, 0, 0, 0xFF}}, // blue
		{pixel(t, img, 1, 0), {0xFF, 0xFF, 0xFF, 0xFF}},
		{pixel(t, img, 0, 1), {0xFF, 0, 0, 0xFF}}, // BGR→RGB：B 分量居前
	}
	// 第三个断言单独算：红像素 BGR = 00 00 FF → RGB 255,0,0
	if got := pixel(t, img, 0, 1); got != (color.RGBA{0xFF, 0, 0, 0xFF}) {
		t.Fatalf("red px: %+v", got)
	}
	if got := pixel(t, img, 1, 1); got != (color.RGBA{0, 0xFF, 0, 0xFF}) {
		t.Fatalf("green px: %+v", got)
	}
	if got := pixel(t, img, 0, 0); got != (color.RGBA{0, 0, 0xFF, 0xFF}) {
		t.Fatalf("blue px: %+v", got)
	}
	_ = want
}

// TestDecodeDIBTopDown：负高 = top-down，行序即图像序。
func TestDecodeDIBTopDown(t *testing.T) {
	px := []byte{
		0xFF, 0x00, 0x00, 0xFF, // (0,0) blue
		0x00, 0xFF, 0x00, 0xFF, // (1,0) green
		0x00, 0x00, 0xFF, 0xFF, // (0,1) red
		0xFF, 0xFF, 0xFF, 0xFF, // (1,1) white
	}
	dib := buildDIB(t, 2, 2, 32, nil, px)
	neg := int32(-2)
	binary.LittleEndian.PutUint32(dib[8:12], uint32(neg)) // top-down
	img, err := decodeDIB(dib)
	if err != nil {
		t.Fatal(err)
	}
	if got := pixel(t, img, 0, 0); got != (color.RGBA{0, 0, 0xFF, 0xFF}) {
		t.Fatalf("(0,0): %+v", got)
	}
	if got := pixel(t, img, 0, 1); got != (color.RGBA{0xFF, 0, 0, 0xFF}) {
		t.Fatalf("(0,1): %+v", got)
	}
}

// TestDecodeDIB24bppRowPadding：24bpp 行按 4 字节对齐（宽 2 → 行 6 字
// 节补 2）。
func TestDecodeDIB24bppRowPadding(t *testing.T) {
	px := []byte{
		0x00, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0, 0, // 红、绿 + 2 padding
		0xFF, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0, 0,
	}
	img, err := decodeDIB(buildDIB(t, 2, 2, 24, nil, px))
	if err != nil {
		t.Fatal(err)
	}
	if got := pixel(t, img, 0, 1); got != (color.RGBA{0xFF, 0, 0, 0xFF}) {
		t.Fatalf("red: %+v", got)
	}
	if got := pixel(t, img, 1, 1); got != (color.RGBA{0, 0xFF, 0, 0xFF}) {
		t.Fatalf("green: %+v", got)
	}
}

// TestDecodeDIB8bppPalette：索引像素经调色板展开（bottom-up：数据行
// 1 是图像顶行）。
func TestDecodeDIB8bppPalette(t *testing.T) {
	pal := []color.RGBA{{0x11, 0x22, 0x33, 0xFF}, {0xAA, 0xBB, 0xCC, 0xFF}}
	px := []byte{
		0, 1, 0, 0, // 底行：idx0 idx1
		1, 0, 0, 0, // 顶行：idx1 idx0
	}
	img, err := decodeDIB(buildDIB(t, 2, 2, 8, pal, px))
	if err != nil {
		t.Fatal(err)
	}
	if got := pixel(t, img, 0, 0); got != (color.RGBA{0xAA, 0xBB, 0xCC, 0xFF}) {
		t.Fatalf("(0,0) idx1: %+v", got)
	}
	if got := pixel(t, img, 1, 0); got != (color.RGBA{0x11, 0x22, 0x33, 0xFF}) {
		t.Fatalf("(1,0) idx0: %+v", got)
	}
	if got := pixel(t, img, 0, 1); got != (color.RGBA{0x11, 0x22, 0x33, 0xFF}) {
		t.Fatalf("(0,1) idx0: %+v", got)
	}
}

// TestDecodeDIBV5Header：BITMAPV5HEADER（124 字节头）同样可解（CF_DIBV5
// 的数据不带罩码时等价于大号 INFOHEADER）。
func TestDecodeDIBV5Header(t *testing.T) {
	hdr := make([]byte, 124)
	binary.LittleEndian.PutUint32(hdr[0:4], 124)
	binary.LittleEndian.PutUint32(hdr[4:8], 1)
	neg1 := int32(-1)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(neg1)) // top-down
	binary.LittleEndian.PutUint16(hdr[12:14], 1)
	binary.LittleEndian.PutUint16(hdr[14:16], 32)
	px := []byte{0x00, 0x00, 0xFF, 0xFF}
	img, err := decodeDIB(append(hdr, px...))
	if err != nil {
		t.Fatal(err)
	}
	if got := pixel(t, img, 0, 0); got != (color.RGBA{0xFF, 0, 0, 0xFF}) {
		t.Fatalf("v5 px: %+v", got)
	}
}

// TestDecodeDIBRejects：截断/不支持压缩/不支持位深 → 错误（放行文本粘
// 贴的路径，不 panic）。
func TestDecodeDIBRejects(t *testing.T) {
	if _, err := decodeDIB(nil); err == nil {
		t.Fatal("nil accepted")
	}
	if _, err := decodeDIB([]byte{1, 2}); err == nil {
		t.Fatal("short accepted")
	}
	dib := buildDIB(t, 1, 1, 32, nil, []byte{0, 0, 0, 0xFF})
	dib = dib[:len(dib)-2] // 像素截断
	if _, err := decodeDIB(dib); err == nil {
		t.Fatal("truncated pixels accepted")
	}
	bad := buildDIB(t, 1, 1, 16, nil, make([]byte, 4))
	binary.LittleEndian.PutUint32(bad[16:20], 2) // BI_RLE
	if _, err := decodeDIB(bad); err == nil {
		t.Fatal("RLE accepted")
	}
}

// TestDIBToPNGRoundTrip：整链 DIB→PNG 编码可被标准库解码回来。
func TestDIBToPNGRoundTrip(t *testing.T) {
	dib := buildDIB(t, 2, 2, 32, nil, []byte{
		0, 0, 0xFF, 0xFF, 0, 0xFF, 0, 0xFF,
		0xFF, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	})
	pngBytes, err := dibToPNG(dib)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pngBytes, pngMagic) {
		t.Fatalf("not png: % x", pngBytes[:8])
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("bounds: %v", img.Bounds())
	}
}
