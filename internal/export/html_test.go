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
