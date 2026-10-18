package export

import (
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// fenceFor 返回能安全包裹 content 的反引号围栏（issue #71：bash 回显
// 常含行首裸 ```，固定三反引号会让内容泄漏且吞掉后续消息）。长度取
// 内容中最长反引号串 +1，最少 3。
func fenceFor(content string) string {
	n, run := 3, 0
	for _, r := range content {
		if r == '`' {
			run++
			if run >= n {
				n = run + 1
			}
			continue
		}
		run = 0
	}
	return strings.Repeat("`", n)
}

// Markdown renders a session transcript as Markdown (the HTML export's
// sibling): plain text in, plain text out — nothing to escape, fences
// preserved verbatim, ready to paste into PRs and docs.
func Markdown(messages []ai.Message, title, model, workspace string) string {
	var b strings.Builder
	head := strings.TrimSpace(title)
	if head == "" {
		head = "会话导出"
	}
	b.WriteString("# " + head + "\n\n")
	b.WriteString("> 模型 " + model + " · 工作区 " + workspace +
		" · 导出于 " + time.Now().Format("2006-01-02 15:04") + "\n\n")
	b.WriteString("---\n\n")

	toolNames := map[string]string{}
	var pendingTools []string
	flush := func() {
		for _, block := range pendingTools {
			b.WriteString(block)
		}
		pendingTools = nil
	}
	for _, m := range messages {
		switch m.Role {
		case ai.RoleUser:
			flush()
			b.WriteString("**你**：\n\n" + strings.TrimRight(m.Text(), "\n") + "\n\n")
			for _, blk := range m.Blocks {
				if src, ok := imageDataURI(blk); ok {
					b.WriteString("![图片附件](" + src + ")\n\n")
				}
			}
		case ai.RoleAssistant:
			flush()
			for _, blk := range m.Blocks {
				switch blk.Kind {
				case ai.BlockThinking:
					if blk.Text == "" {
						continue
					}
					var q strings.Builder
					for _, line := range strings.Split(strings.TrimRight(blk.Text, "\n"), "\n") {
						q.WriteString("> " + line + "\n")
					}
					b.WriteString("<details><summary>思考过程</summary>\n\n" +
						q.String() + "\n</details>\n\n")
				case ai.BlockToolCall:
					toolNames[blk.ID] = blk.Name
				case ai.BlockText:
					if blk.Text == "" {
						continue
					}
					b.WriteString(strings.TrimRight(blk.Text, "\n") + "\n\n")
				}
			}
		case ai.RoleToolResult:
			for _, blk := range m.Blocks {
				if blk.Kind != ai.BlockToolResult {
					continue
				}
				name := toolNames[blk.ToolCallID]
				if name == "" {
					name = "tool"
				}
				mark := ""
				if blk.IsError {
					mark = "（失败）"
				}
				var out strings.Builder
				out.WriteString(fenceFor(blk.Text) + "text\n" +
					strings.TrimRight(blk.Text, "\n") + "\n" + fenceFor(blk.Text) + "\n\n")
				pendingTools = append(pendingTools,
					"**"+name+"**"+mark+"：\n\n"+out.String())
			}
		}
	}
	flush()
	return strings.TrimRight(b.String(), "\n") + "\n"
}
