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
