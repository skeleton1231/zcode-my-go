package webfetch

import (
	"html"
	"regexp"
	"strings"
)

// 内容处理口径（ZCode 同款）：响应 10MB 封顶、给模型 100K 字符封顶。
const (
	MaxResponseBytes = 10 << 20
	MaxModelChars    = 100_000
)

// ExtractContent 按内容类型抽取可读文本：HTML/xhtml 走标签剥离 + 实体
// 解码的 Markdown 化；其余文本类原样 trim；非文本类报错（webfetch 不
// 取二进制——图片走附件路径）。
func ExtractContent(body []byte, contentType string) (string, error) {
	mime := contentTypeMime(contentType)
	if !isTextLikeMime(mime) {
		return "", errUnsupportedContent(mime)
	}
	text := string(body)
	if mime == "text/html" || mime == "application/xhtml+xml" || strings.Contains(contentType, "html") {
		return htmlToText(text), nil
	}
	return strings.TrimSpace(text), nil
}

// contentTypeMime 取 Content-Type 的 MIME 部分（去参数、小写）。
func contentTypeMime(contentType string) string {
	mime := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	return strings.ToLower(mime)
}

func isTextLikeMime(mime string) bool {
	if mime == "" {
		return true // 无类型按文本容忍（服务器没说就当文本）
	}
	return strings.HasPrefix(mime, "text/") ||
		mime == "application/json" || mime == "application/xml" ||
		mime == "application/xhtml+xml" ||
		mime == "application/javascript" || mime == "application/x-javascript" ||
		strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
}

func errUnsupportedContent(mime string) error {
	return &ContentError{Mime: mime}
}

// ContentError 报告非文本内容类型（调用方转成模型可自纠的错误结果）。
type ContentError struct{ Mime string }

func (e *ContentError) Error() string {
	return "webfetch: 不支持的内容类型 " + e.Mime
}

// TruncateForModel 封顶模型可见文本，超限标注。
func TruncateForModel(content string) (string, bool) {
	runes := []rune(content)
	if len(runes) <= MaxModelChars {
		return content, false
	}
	marker := "\n\n[webfetch：内容已截断]"
	cut := MaxModelChars - len([]rune(marker))
	return string(runes[:cut]) + marker, true
}

// 标签处理：注释/脚本/样式先整段剥掉，结构标签转行，其余标签删除。
var (
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reScript   = regexp.MustCompile(`(?is)<script\b.*?</script>`)
	reStyle    = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	reNoscript = regexp.MustCompile(`(?is)<noscript\b.*?</noscript>`)
	reLink     = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reListItem = regexp.MustCompile(`(?is)<li\b[^>]*>(.*?)</li>`)
	reBR       = regexp.MustCompile(`(?i)<br\s*/?>`)
	reBlockEnd = regexp.MustCompile(`(?i)</(?:p|div|section|article|header|footer|tr|table|ul|ol|li|pre|blockquote)>`)
	reSpaces   = regexp.MustCompile(`[ \t]+`)
	reAnyTag   = regexp.MustCompile(`<[^>]*>`)
	// headingRes 按级别替换 <hN> 为 N 个 # 的 Markdown 标题。
	headingRes = map[string]*regexp.Regexp{
		"1": regexp.MustCompile(`(?is)<h1\b[^>]*>(.*?)</h1>`),
		"2": regexp.MustCompile(`(?is)<h2\b[^>]*>(.*?)</h2>`),
		"3": regexp.MustCompile(`(?is)<h3\b[^>]*>(.*?)</h3>`),
		"4": regexp.MustCompile(`(?is)<h4\b[^>]*>(.*?)</h4>`),
		"5": regexp.MustCompile(`(?is)<h5\b[^>]*>(.*?)</h5>`),
		"6": regexp.MustCompile(`(?is)<h6\b[^>]*>(.*?)</h6>`),
	}
)

// htmlToText 把 HTML 抽成可读纯文本（标题/链接/列表保形，实体解码，
// 空行折叠）。够用于模型阅读文档正文，不追求完整 Markdown 保真。
func htmlToText(text string) string {
	s := reComment.ReplaceAllString(text, "")
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reNoscript.ReplaceAllString(s, "")
	for level, re := range headingRes {
		hashes := strings.Repeat("#", int(level[0]-'0'))
		s = re.ReplaceAllString(s, "\n"+hashes+" $1\n")
	}
	s = reLink.ReplaceAllString(s, "[$2]($1)")
	s = reListItem.ReplaceAllString(s, "\n- $1")
	s = reBR.ReplaceAllString(s, "\n")
	s = reBlockEnd.ReplaceAllString(s, "\n")
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		line = strings.TrimSpace(reSpaces.ReplaceAllString(line, " "))
		if line == "" {
			if !prevBlank {
				out = append(out, "")
				prevBlank = true
			}
			continue
		}
		out = append(out, line)
		prevBlank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
