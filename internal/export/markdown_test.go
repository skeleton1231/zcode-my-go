package export

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestMarkdownStructures(t *testing.T) {
	out := Markdown(sampleMessages(), "调试记录", "glm-5.2", `D:\ws`)
	for _, want := range []string{
		"# 调试记录",
		"**你**：",
		"**read**：",
		"```text\n1\tone",
		"```go\nfmt.Println(\"hi\")",
		"> 模型 glm-5.2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	// 工具失败标记。
	failing := []ai.Message{
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockToolCall, ID: "c9", Name: "bash", Arguments: "{}"},
		}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			{Kind: ai.BlockToolResult, ToolCallID: "c9", Text: "exit 1", IsError: true},
		}},
	}
	if out := Markdown(failing, "x", "m", "w"); !strings.Contains(out, "**bash**（失败）：") {
		t.Fatalf("failure mark missing:\n%s", out)
	}
	// 思考过程折叠块。
	thinking := []ai.Message{
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockThinking, Text: "先想\n再想"},
			{Kind: ai.BlockText, Text: "答案"},
		}},
	}
	if out := Markdown(thinking, "x", "m", "w"); !strings.Contains(out, "<details><summary>思考过程</summary>") ||
		!strings.Contains(out, "> 再想") {
		t.Fatalf("thinking block:\n%s", out)
	}
}

func TestMarkdownEmptyAndFences(t *testing.T) {
	if out := Markdown(nil, "", "m", "w"); !strings.Contains(out, "# 会话导出") {
		t.Fatalf("empty export broken")
	}
	// 未闭合围栏原样保留（导出不修复内容，只搬运）。
	unclosed := []ai.Message{{Role: ai.RoleAssistant, Blocks: []ai.Block{
		{Kind: ai.BlockText, Text: "```go\nunclosed"},
	}}}
	if out := Markdown(unclosed, "x", "m", "w"); !strings.Contains(out, "unclosed") {
		t.Fatal("unclosed fence content lost")
	}
}

// TestMarkdownNestedFences 验证工具结果含行首裸围栏时动态加长围栏
// （issue #71：固定 ``` 会让内容泄漏 + 后续消息被吞）。
func TestMarkdownNestedFences(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "cat 一下"}}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockToolCall, ID: "c1", Name: "bash", Arguments: `{}`}}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			// bash 回显：行首裸 ``` 与 ````，无行号前缀。
			{Kind: ai.BlockToolResult, ToolCallID: "c1",
				Text: "# Title\n```go\nfmt\n```\n````inner\n正文仍在输出里\n```"},
		}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "后续回答必须可见"}}},
	}
	out := Markdown(msgs, "x", "m", "w")
	// 后续消息不得被吞。
	if !strings.Contains(out, "后续回答必须可见") {
		t.Fatal("following message swallowed by fence collision")
	}
	if !strings.Contains(out, "正文仍在输出里") {
		t.Fatal("tool output content lost")
	}
	// 围栏动态加长：至少出现四反引号围栏包住含 ``` 的内容。
	if !strings.Contains(out, "````text") {
		t.Fatalf("dynamic fence missing:\n%s", out)
	}
}

// TestMarkdownExportEmbedsImages 验证用户图片块导出为 data URI 图片链
// 接（自包含分享物），空数据块跳过。
func TestMarkdownExportEmbedsImages(t *testing.T) {
	msgs := []ai.Message{{
		Role: ai.RoleUser,
		Blocks: []ai.Block{
			{Kind: ai.BlockText, Text: "截图如下"},
			{Kind: ai.BlockImage, Media: "image/png", Data: "aWNvbmc="},
			{Kind: ai.BlockImage, Media: "image/png", Data: ""}, // 空数据跳过
		},
	}}
	got := Markdown(msgs, "t", "m", "/w")
	if !strings.Contains(got, "![图片附件](data:image/png;base64,aWNvbmc=)") {
		t.Fatalf("image link missing:\n%s", got)
	}
	if strings.Count(got, "![图片附件]") != 1 {
		t.Fatalf("empty block emitted: %q", got)
	}
}

// TestMarkdownExportDiff 验证工具 diff 的 ```diff 围栏（spec 规则 34）。
func TestMarkdownExportDiff(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockToolCall, ID: "c1", Name: "write", Arguments: "{}"},
		}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			{Kind: ai.BlockToolResult, ToolCallID: "c1", Text: "wrote",
				Diff: "+hello\n-world"},
		}},
	}
	got := Markdown(msgs, "t", "m", "/w")
	if !strings.Contains(got, "```diff\n+hello\n-world\n```") {
		t.Fatalf("diff fence missing:\n%s", got)
	}
}
