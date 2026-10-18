package export

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func sampleMessages() []ai.Message {
	return []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "解释 <script>alert(1)</script> 这段"}}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockToolCall, ID: "c1", Name: "read", Arguments: `{"path":"a.go"}`},
		}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			{Kind: ai.BlockToolResult, ToolCallID: "c1", Text: "1\tone\n2\ttwo"},
		}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText,
			Text: "结果：\n```go\nfmt.Println(\"hi\")\n```\n完成"}}},
	}
}

func TestHTMLEscapesAndStructures(t *testing.T) {
	out := HTML(sampleMessages(), "会话", "glm-5.2", `D:\ws`)
	for _, want := range []string{
		"<title>会话</title>",
		"模型 glm-5.2",
		"&lt;script&gt;alert(1)&lt;/script&gt;", // 用户输入被转义
		"<details class=\"tool",                 // 工具块折叠
		"1\tone",                                // 工具输出保留
		"<pre><code>fmt.Println(&#34;hi&#34;)",  // 代码围栏成块且转义
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export missing %q", want)
		}
	}
	// 注入不得以可执行形式出现。
	if strings.Contains(out, "<script>alert") {
		t.Fatal("unescaped script tag in export")
	}
}

func TestHTMLToleratesEmpty(t *testing.T) {
	out := HTML(nil, "空", "m", "ws")
	if !strings.Contains(out, "<title>空</title>") {
		t.Fatal("empty export broken")
	}
}

// TestHTMLToolSummaryNames 验证工具条目的折叠标题携带工具名与参数
// （issue #73：ToolName/ToolArgs 恒为零值，summary 渲染成空 " · "）。
func TestHTMLToolSummaryNames(t *testing.T) {
	out := HTML(sampleMessages(), "t", "m", "ws")
	// 模板自动转义：引号呈 &#34; 形态。
	if !strings.Contains(out, "read · ") || !strings.Contains(out, "{&#34;path&#34;:") {
		t.Fatal("tool summary lacks name/args")
	}
}

// TestHTMLExportEmbedsImages 验证用户图片块内嵌为 data URI（spec
// desktop-shell 规则 22）：png 附件出现在 img src，非 image/* 媒体跳过。
func TestHTMLExportEmbedsImages(t *testing.T) {
	msgs := []ai.Message{{
		Role: ai.RoleUser,
		Blocks: []ai.Block{
			{Kind: ai.BlockText, Text: "看这张图"},
			{Kind: ai.BlockImage, Media: "image/png", Data: "aWNvbmc="},
			{Kind: ai.BlockImage, Media: "text/html", Data: "eG1s"}, // 坏媒体跳过
		},
	}}
	got := HTML(msgs, "t", "m", "/w")
	if !strings.Contains(got, `src="data:image/png;base64,aWNvbmc="`) {
		t.Fatalf("png data URI missing:\n%s", got)
	}
	if strings.Contains(got, "text/html") || strings.Contains(got, "eG1s") {
		t.Fatal("non-image media leaked into export")
	}
	if !strings.Contains(got, "看这张图") {
		t.Fatal("user text lost")
	}
}

// TestHTMLExportThinkingAndDiff 验证导出完整性（spec 规则 34）：
// thinking 块折叠呈现、tool-result 携带的 diff 彩色渲染（+绿/-红）。
func TestHTMLExportThinkingAndDiff(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "改一下"}}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockThinking, Text: "先看文件"},
			{Kind: ai.BlockToolCall, ID: "c1", Name: "edit", Arguments: `{"path":"a"}`},
		}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			{Kind: ai.BlockToolResult, ToolCallID: "c1", Text: "edited",
				Diff: "--- a\n+++ b\n-old\n+new\n ctx"},
		}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "完成"}}},
	}
	got := HTML(msgs, "t", "m", "/w")
	if !strings.Contains(got, "思考过程") || !strings.Contains(got, "先看文件") {
		t.Fatalf("thinking block missing:\n%.600s", got)
	}
	if !strings.Contains(got, `class="add">+new`) || !strings.Contains(got, `class="del">-old`) {
		t.Fatalf("diff coloring missing:\n%.800s", got)
	}
	if !strings.Contains(got, "ctx") { // 上下文行不着色
		t.Fatalf("context line lost:\n%.800s", got)
	}
}
