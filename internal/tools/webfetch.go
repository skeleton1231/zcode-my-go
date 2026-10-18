package tools

import (
	"context"
	"encoding/json"

	"fmt"
	"strings"
	"sync"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/webfetch"
)

// webfetchTool 抓取 URL 并抽取文本（ZCode 的 WebFetch）：预批准文档站
// 只读自动放行；其余主机过权限询问。prompt 是可选的处理指引——随结
// 果回显，由模型对内容执行（工具内不做模型调用）。
type webfetchTool struct {
	fetcher func() *webfetch.Fetcher // 每次执行取（共享缓存挂在服务上）
}

// NewWebFetchTool 构建工具；fetcher 在每次执行时求值（服务持有共享
// Fetcher——缓存跨会话生效）。
func NewWebFetchTool(fetcher func() *webfetch.Fetcher) agent.Tool {
	return &webfetchTool{fetcher: fetcher}
}

// Name implements agent.Tool.
func (t *webfetchTool) Name() string { return "webfetch" }

// Description implements agent.Tool.
func (t *webfetchTool) Description() string {
	return "Fetch an http(s) URL and return readable text (HTML is " +
		"converted, scripts/styles stripped). Use for documentation and " +
		"reference pages. Redirects to other hosts are reported, not " +
		"followed automatically. Optional prompt says what to extract " +
		"from the content."
}

// Parameters implements agent.Tool.
func (t *webfetchTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"url":    prop("string", "the http(s) URL to fetch"),
		"prompt": prop("string", "optional: what to look for in the content"),
	}, "url")
}

// Execute implements agent.Tool.
func (t *webfetchTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	raw := strings.TrimSpace(a.URL)
	if raw == "" {
		return errResult("url is required")
	}
	res, err := t.fetcher().Fetch(ctx, raw)
	if err != nil {
		return errResult("%v", err)
	}
	return agent.ToolResult{Content: formatWebFetch(res, strings.TrimSpace(a.Prompt))}
}

// Capability implements agent.Capabler：未知形态按中等风险（询问）。
func (t *webfetchTool) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// CapabilityForArgs implements agent.ArgsCapabler：预批准文档站是只读低
// 风险（自动放行，plan 模式可用）；其余主机出网是外部副作用——询问
// （会话「总是允许」按 URL 积累，项目规则可配 webfetch 通配）。
func (t *webfetchTool) CapabilityForArgs(args string) permission.Capability {
	var a struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return t.Capability()
	}
	if webfetch.IsPreapprovedURL(strings.TrimSpace(a.URL)) {
		return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
	}
	return permission.Capability{Risk: permission.RiskMedium}
}

// formatWebFetch 渲染结果：元信息头 + 正文（+ prompt 指引回显）。
func formatWebFetch(res *webfetch.Result, prompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\n状态: %d  类型: %s  大小: %d bytes  耗时: %dms",
		res.FinalURL, res.Status, res.ContentType, res.Bytes, res.DurationMs)
	if res.CacheHit {
		b.WriteString("（缓存命中）")
	}
	if len(res.Redirects) > 0 {
		b.WriteString("\n重定向链：")
		for _, r := range res.Redirects {
			fmt.Fprintf(&b, "\n  %d %s → %s", r.Code, r.From, r.To)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(res.Content)
	if prompt != "" {
		b.WriteString("\n\n（按提示处理：）" + prompt)
	}
	return b.String()
}

// sharedWebfetch 是进程级共享 Fetcher（缓存跨会话/表面共用）。
var sharedWebfetch struct {
	once sync.Once
	f    *webfetch.Fetcher
}

// WebFetcher 返回共享 Fetcher（惰性构建）。
func WebFetcher() *webfetch.Fetcher {
	sharedWebfetch.once.Do(func() { sharedWebfetch.f = webfetch.NewFetcher() })
	return sharedWebfetch.f
}
