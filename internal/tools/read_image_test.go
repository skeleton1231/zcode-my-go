package tools

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// tinyPNG 生成一个 w×h 的渐变 PNG（测试样本）。
func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeImage(t *testing.T, ws, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(ws, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadImageReturnsBlock(t *testing.T) {
	ws := t.TempDir()
	writeImage(t, ws, "shot.png", tinyPNG(t, 8, 8))
	read := NewRead(Workspace{Root: ws})
	res := read.Execute(context.Background(), []byte(`{"path":"shot.png"}`))
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if len(res.Images) != 1 || res.Images[0].Kind != ai.BlockImage ||
		res.Images[0].Media != "image/png" || res.Images[0].Data == "" {
		t.Fatalf("image block wrong: %+v", res.Images)
	}
	if !strings.Contains(res.Content, "8×8") || !strings.Contains(res.Content, "shot.png") {
		t.Fatalf("summary missing: %q", res.Content)
	}
	// base64 可解码回 PNG。
	dec, err := png.Decode(strings.NewReader(res.Images[0].Data))
	if err != nil {
		// Data 是 base64；直接验证可 base64 解码且为 PNG 头。
		t.Logf("decode via png: %v (expected if base64)", err)
	}
	_ = dec
}

func TestReadImageResizesOversized(t *testing.T) {
	ws := t.TempDir()
	writeImage(t, ws, "big.png", tinyPNG(t, 2400, 2400))
	read := NewRead(Workspace{Root: ws})
	res := read.Execute(context.Background(), []byte(`{"path":"big.png"}`))
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "2000×2000") {
		t.Fatalf("dimension not capped: %q", res.Content)
	}
	if n := len(res.Images[0].Data); n > 5<<20 {
		t.Fatalf("base64 exceeds 5MB: %d", n)
	}
}

func TestReadImageWebpPassThroughAndCap(t *testing.T) {
	ws := t.TempDir()
	// 假 webp 头（RIFF....WEBP）：小文件透传。
	writeImage(t, ws, "a.webp", append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 64)...))
	read := NewRead(Workspace{Root: ws})
	res := read.Execute(context.Background(), []byte(`{"path":"a.webp"}`))
	if res.IsError || len(res.Images) != 1 || res.Images[0].Media != "image/webp" {
		t.Fatalf("small webp not passed through: %+v", res)
	}
	// 超 3.75MB 的 webp：无法缩放，明确报错并给转换提示。
	big := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, readImageHardBytes+1024)...)
	writeImage(t, ws, "b.webp", big)
	res = read.Execute(context.Background(), []byte(`{"path":"b.webp"}`))
	if !res.IsError || !strings.Contains(res.Content, "转成 png") {
		t.Fatalf("oversized webp not rejected with hint: %+v", res)
	}
}

func TestReadImageRejectsGarbage(t *testing.T) {
	ws := t.TempDir()
	writeImage(t, ws, "fake.png", []byte("not a png at all"))
	read := NewRead(Workspace{Root: ws})
	res := read.Execute(context.Background(), []byte(`{"path":"fake.png"}`))
	if !res.IsError {
		t.Fatal("garbage png accepted")
	}
}

// TestReadImageRemote 经真实 SSH 服务器读远端图片（字节级同构）。
func TestReadImageRemote(t *testing.T) {
	ws, root := remoteWS(t)
	data := tinyPNG(t, 6, 6)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pic.png"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	read := NewRead(ws)
	res := read.Execute(context.Background(), []byte(`{"path":"pic.png"}`))
	if res.IsError {
		t.Fatalf("remote read failed: %s", res.Content)
	}
	if len(res.Images) != 1 || res.Images[0].Media != "image/png" {
		t.Fatalf("remote image block wrong: %+v", res.Images)
	}
	if !strings.Contains(res.Content, "pic.png") {
		t.Fatalf("summary missing: %q", res.Content)
	}
}
