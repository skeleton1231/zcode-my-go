// Package export renders a session transcript as a standalone HTML file
// (ZCode/pi's export-html, sized for Zgo): one self-contained document,
// no external assets, light/dark by preference, tool calls collapsed.
package export

import (
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// Entry is one rendered row of the timeline.
type Entry struct {
	Role       string // user | assistant | tool
	Text       template.HTML
	Images     []template.URL // 用户消息的图片附件（内嵌 data URI）
	Thinking   template.HTML  // assistant 的思考块（折叠，spec 规则 34）
	ToolName   string
	ToolArgs   string
	ToolOutput template.HTML
	ToolDiff   template.HTML // 改动类工具的统一 diff（彩色行）
	ToolError  bool
}

// Page carries the template's data.
type Page struct {
	Title     string
	Model     string
	Workspace string
	When      string
	Entries   []Entry
}

var pageTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; --bg:#f6f6f7; --card:#ffffff; --border:#d9d9de;
  --text:#1c1c1f; --muted:#6d6d74; --accent:#3a6df0; --danger:#d4383d;
  --code:#f0f0f2; --radius:10px; }
@media (prefers-color-scheme: dark) { :root { --bg:#17171a; --card:#202024;
  --border:#33333a; --text:#e8e8ec; --muted:#9d9da6; --accent:#6b8ffb;
  --danger:#ef6b6f; --code:#26262b; } }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--text);
  font:15px/1.65 system-ui,"Segoe UI","Microsoft YaHei",sans-serif;
  -webkit-font-smoothing: antialiased; }
.wrap { max-width:860px; margin:0 auto; padding:32px 24px 64px; }
header { border-bottom:1px solid var(--border); padding-bottom:16px; margin-bottom:24px; }
h1 { font-size:20px; margin:0 0 6px; }
.meta { color:var(--muted); font-size:13px; }
.entry { margin:0 0 20px; }
.user { display:flex; justify-content:flex-end; }
.user .bubble { background:color-mix(in srgb, var(--accent) 12%, transparent);
  padding:10px 14px; border-radius:var(--radius); max-width:80%;
  white-space:pre-wrap; overflow-wrap:anywhere; }
.assistant { white-space:normal; overflow-wrap:anywhere; }
.assistant pre { background:var(--code); border-radius:8px; padding:12px 14px;
  overflow-x:auto; font-size:13px; }
.assistant p { margin:0 0 8px; }
details.tool { background:var(--card); border:1px solid var(--border);
  border-radius:var(--radius); }
details.tool summary { cursor:pointer; padding:8px 12px; font-size:13px;
  color:var(--muted); font-family:ui-monospace,Consolas,monospace; }
details.tool summary .err { color:var(--danger); }
details.tool pre { margin:0; padding:10px 12px; border-top:1px solid var(--border);
  max-height:320px; overflow:auto; font-size:12px; color:var(--muted);
  font-family:ui-monospace,Consolas,monospace; white-space:pre; }
</style>
</head>
<body>
<div class="wrap">
<header>
  <h1>{{.Title}}</h1>
  <div class="meta">模型 {{.Model}} · 工作区 {{.Workspace}} · 导出于 {{.When}}</div>
</header>
{{range .Entries}}
  {{if eq .Role "user"}}<div class="entry user"><div class="bubble">{{.Text}}</div>{{range .Images}}<img class="attach" src="{{.}}" alt="图片附件">{{end}}</div>
  {{else if eq .Role "assistant"}}{{if .Thinking}}{{.Thinking}}{{end}}<div class="entry assistant">{{.Text}}</div>
  {{else}}<details class="tool entry"><summary>{{.ToolName}} · {{.ToolArgs}}{{if .ToolError}} <span class="err">✕</span>{{end}}</summary><pre>{{.ToolOutput}}</pre>{{if .ToolDiff}}<pre class="diff">{{.ToolDiff}}</pre>{{end}}</details>{{end}}
{{end}}
</div>
</body>
</html>
`))

// HTML renders the transcript. Assistant text keeps its fenced code
// blocks; everything else is escaped by html/template.
func HTML(messages []ai.Message, title, model, workspace string) string {
	page := Page{
		Title: title, Model: model, Workspace: workspace,
		When: time.Now().Format("2006-01-02 15:04"),
	}
	var pendingTools []Entry
	// 工具名/参数按 ID 从 assistant 的 ToolCall 块建映射（issue #73：
	// 移植自 markdown.go 时丢了这一步，summary 恒渲染成空 " · "）。
	toolCalls := map[string]ai.Block{}
	flush := func() {
		page.Entries = append(page.Entries, pendingTools...)
		pendingTools = nil
	}
	for _, m := range messages {
		switch m.Role {
		case ai.RoleUser:
			flush()
			e := Entry{Role: "user",
				Text: template.HTML(html.EscapeString(m.Text()))}
			for _, b := range m.Blocks {
				if src, ok := imageDataURI(b); ok {
					// template.URL：src 由我们构造（media 必须 image/*、
					// data 是 base64），绕过模板的 URL 过滤避免
					// ZgotmplZ 替换。
					e.Images = append(e.Images, template.URL(src))
				}
			}
			page.Entries = append(page.Entries, e)
		case ai.RoleAssistant:
			flush()
			thinking := ""
			for _, b := range m.Blocks {
				if b.Kind == ai.BlockThinking && b.Text != "" {
					thinking += b.Text + "\n"
				}
			}
			for _, b := range m.Blocks {
				if b.Kind == ai.BlockToolCall {
					toolCalls[b.ID] = b
				}
			}
			e := Entry{Role: "assistant", Text: renderMarkdown(m.Text())}
			if thinking != "" {
				// 与 Markdown 的 <details> 对称（spec 规则 34）。
				e.Thinking = template.HTML("<details class=\"thinking\"><summary>思考过程</summary><pre>" +
					html.EscapeString(strings.TrimRight(thinking, "\n")) + "</pre></details>")
			}
			page.Entries = append(page.Entries, e)
		case ai.RoleToolResult:
			for _, b := range m.Blocks {
				if b.Kind != ai.BlockToolResult {
					continue
				}
				call := toolCalls[b.ToolCallID]
				pendingTools = append(pendingTools, Entry{
					Role:       "tool",
					ToolName:   call.Name,
					ToolArgs:   call.Arguments,
					ToolOutput: template.HTML(html.EscapeString(b.Text)),
					ToolDiff:   template.HTML(renderDiffHTML(b.Diff)),
					ToolError:  b.IsError,
				})
			}
		}
	}
	flush()
	var b strings.Builder
	if err := pageTmpl.Execute(&b, page); err != nil {
		// 模板是静态的，出错只可能来自数据类型——退化为纯转义文本。
		return "<pre>" + html.EscapeString(err.Error()) + "</pre>"
	}
	return b.String()
}

// renderMarkdown keeps fenced code blocks as <pre> and escapes everything
// else (the same contract as the desktop renderer).
func renderMarkdown(text string) template.HTML {
	var b strings.Builder
	var inCode bool
	var code strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCode {
				b.WriteString("<pre><code>" + html.EscapeString(code.String()) + "</code></pre>")
				code.Reset()
			}
			inCode = !inCode
			continue
		}
		if inCode {
			code.WriteString(line + "\n")
			continue
		}
		b.WriteString("<p>" + html.EscapeString(line) + "</p>")
	}
	if inCode { // 未闭合的围栏按代码块收尾
		b.WriteString("<pre><code>" + html.EscapeString(code.String()) + "</code></pre>")
	}
	return template.HTML(b.String())
}

// imageDataURI 把图片块变成内嵌 data URI（自包含分享物，spec 规则
// 22）；非 image/* 媒体或空数据返回 false 跳过——导出不因坏块失败。
func imageDataURI(b ai.Block) (string, bool) {
	if b.Kind != ai.BlockImage || b.Data == "" {
		return "", false
	}
	media := b.Media
	if media == "" {
		media = "image/png"
	}
	if !strings.HasPrefix(media, "image/") {
		return "", false
	}
	return "data:" + media + ";base64," + b.Data, true
}

// renderDiffHTML renders a unified diff with per-line coloring（+绿/-
// 红，spec 规则 34）——输出已转义，经 ToolDiff 的 template.HTML 直达。
func renderDiffHTML(diff string) string {
	if diff == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		esc := html.EscapeString(line)
		switch {
		case strings.HasPrefix(line, "+"):
			b.WriteString(`<span class="add">` + esc + "</span>\n")
		case strings.HasPrefix(line, "-"):
			b.WriteString(`<span class="del">` + esc + "</span>\n")
		default:
			b.WriteString(esc + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
