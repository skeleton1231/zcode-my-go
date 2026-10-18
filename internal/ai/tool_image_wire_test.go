package ai

// 工具结果携带图片块（spec 规则 47）的三协议 wire：Anthropic 混排进
// user 消息、Gemini 以 inlineData part 同住 user content、OpenAI 无法
// 承载——tool 消息文本追加降级说明。

import (
	"strings"
	"testing"
)

func imageToolResultMessages() []Message {
	return []Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "看这张图"}}},
		{Role: RoleAssistant, Blocks: []Block{
			{Kind: BlockToolCall, ID: "c1", Name: "read", Arguments: `{"path":"s.png"}`},
		}},
		{Role: RoleToolResult, Blocks: []Block{
			{Kind: BlockToolResult, ToolCallID: "c1", Text: "图片 s.png：image/png，4×4。"},
			{Kind: BlockImage, Media: "image/png", Data: "aGk="},
		}},
	}
}

func TestAnthropicToolResultImageWire(t *testing.T) {
	wire := toWire(imageToolResultMessages())
	if len(wire) != 3 {
		t.Fatalf("wire = %+v", wire)
	}
	tr := wire[2]
	if tr.Role != "user" || len(tr.Content) != 2 {
		t.Fatalf("tool result message = %+v", tr)
	}
	if tr.Content[0].Type != "tool_result" || tr.Content[0].ToolUseID != "c1" {
		t.Fatalf("tool_result block = %+v", tr.Content[0])
	}
	img := tr.Content[1]
	if img.Type != "image" || img.Source == nil ||
		img.Source.MediaType != "image/png" || img.Source.Data != "aGk=" {
		t.Fatalf("image block = %+v", img)
	}
}

func TestGeminiToolResultImageWire(t *testing.T) {
	msgs := toGemini(imageToolResultMessages())
	// 最后一条是 user role 的 functionResponse 消息，图片以 inlineData
	// part 同住其中。
	last := msgs[len(msgs)-1]
	if last.Role != "user" || len(last.Parts) != 2 {
		t.Fatalf("gemini tool result = %+v", last)
	}
	if last.Parts[0].FunctionResponse.Name != "read" {
		t.Fatalf("functionResponse part = %+v", last.Parts[0])
	}
	if last.Parts[1].InlineData == nil ||
		last.Parts[1].InlineData.MimeType != "image/png" ||
		last.Parts[1].InlineData.Data != "aGk=" {
		t.Fatalf("inline image part = %+v", last.Parts[1])
	}
}

func TestOpenAIToolResultImageDegrades(t *testing.T) {
	msgs := toOpenAI("sys", imageToolResultMessages())
	var tool *openaiMessage
	for i := range msgs {
		if msgs[i].Role == "tool" {
			tool = &msgs[i]
		}
	}
	if tool == nil {
		t.Fatalf("no tool message: %+v", msgs)
	}
	if tool.ToolCallID != "c1" {
		t.Fatalf("tool call id = %+v", tool)
	}
	content, _ := tool.Content.(string)
	if !strings.Contains(content, "图片未上送") {
		t.Fatalf("degradation note missing: %q", content)
	}
}
