package e2e

// read 图片（spec 规则 47）的全栈 e2e：真实 read 工具读工作区图片，
// 图片块经 loop 进入历史并出现在第二次 provider 请求。

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestE2EReadImageReachesProvider(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"shot.png"}`}}},
		Turn{Text: "done"},
	)
	// 生成一张真 PNG 进工作区。
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 20), B: 0x40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Workspace, "shot.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	events := h.Ask("看看这张图")

	saw := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "read" && !ev.IsError {
			if bytes.Contains([]byte(ev.ResultContent), []byte("shot.png")) {
				saw = true
			}
		}
	}
	if !saw {
		t.Fatal("read tool did not summarize the image")
	}
	if len(h.Script.Requests) < 2 {
		t.Fatal("no second provider request")
	}
	found := false
	for _, m := range h.Script.Requests[1].Messages {
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockImage && b.Media == "image/png" && b.Data != "" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("image block never reached the provider")
	}
}
