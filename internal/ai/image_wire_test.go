package ai

import (
	"encoding/json"
	"testing"
)

// imgMsg builds a user message with text + one image block.
func imgMsg() Message {
	return Message{Role: RoleUser, Blocks: []Block{
		{Kind: BlockText, Text: "看看这张图"},
		{Kind: BlockImage, Media: "image/png", Data: "aGk="},
	}}
}

// TestAnthropicImageWire: the image rides as a base64 source block next to
// the text, in order.
func TestAnthropicImageWire(t *testing.T) {
	wire := toWire([]Message{imgMsg()})
	b, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	content := parsed[0]["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v", content)
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Fatal("text block not first")
	}
	img := content[1].(map[string]any)
	if img["type"] != "image" {
		t.Fatalf("image type = %v", img["type"])
	}
	src := img["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "aGk=" {
		t.Fatalf("source = %v", src)
	}
}

// TestOpenAIImageWire: multimodal user content — text part plus a
// data-URL image_url part; text-only messages stay plain strings.
func TestOpenAIImageWire(t *testing.T) {
	msgs := toOpenAI("", []Message{imgMsg()})
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	parts := parsed[0]["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("content = %v", parts)
	}
	if parts[0].(map[string]any)["type"] != "text" {
		t.Fatal("text part not first")
	}
	img := parts[1].(map[string]any)
	if img["type"] != "image_url" {
		t.Fatalf("part type = %v", img["type"])
	}
	url := img["image_url"].(map[string]any)["url"].(string)
	if url != "data:image/png;base64,aGk=" {
		t.Fatalf("url = %q", url)
	}
	// 纯文本消息仍是字符串 content（不变式：无图不动格式）。
	plain := toOpenAI("", []Message{{Role: RoleUser,
		Blocks: []Block{{Kind: BlockText, Text: "hi"}}}})
	pb, _ := json.Marshal(plain)
	if string(pb) == "" || plain[0].Content.(string) != "hi" {
		t.Fatalf("plain content = %v", plain[0].Content)
	}
}

// TestGeminiImageWire: inlineData part with mime and data.
func TestGeminiImageWire(t *testing.T) {
	out := toGemini([]Message{imgMsg()})
	if len(out) != 1 || len(out[0].Parts) != 2 {
		t.Fatalf("out = %+v", out)
	}
	img := out[0].Parts[1]
	if img.InlineData == nil || img.InlineData.MimeType != "image/png" ||
		img.InlineData.Data != "aGk=" {
		t.Fatalf("inline = %+v", img.InlineData)
	}
	if out[0].Parts[0].Text != "看看这张图" {
		t.Fatalf("text part = %q", out[0].Parts[0].Text)
	}
}
