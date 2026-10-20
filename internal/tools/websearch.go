package tools

// websearch 工具（spec 规则 121，CC 工具族 WebSearch 的对应）：固定
// 端点搜索（默认 DDG 无 key，settings.search 可切 Brave），命中列表
// 文本化——标题/URL/摘录。不回访 URL：深挖走 webfetch（那边有 egress
// 守卫与权限询问面）。输出上限 8KB（搜索结果是导航信息，超过即截断）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/websearch"
)

// websearchHead 是输出截断上限（只截头——条目自上而下按相关度排）。
const websearchHead = 8 << 10

// websearchTool 每次执行经 get 取后端（服务层从 settings 即时构建，
// provider/key 改配置下回合生效；测试注入假端点）。
type websearchTool struct {
	get func() *websearch.Searcher
}

// NewWebSearchTool 构建工具；get 为 nil 时执行报未接线（防御——与
// gopls 组同款约定）。
func NewWebSearchTool(get func() *websearch.Searcher) agent.Tool {
	return &websearchTool{get: get}
}

// Name implements agent.Tool.
func (t *websearchTool) Name() string { return "websearch" }

// Description implements agent.Tool.
func (t *websearchTool) Description() string {
	return "Search the web and return up to 10 results (title, URL, " +
		"snippet). Use it to discover pages, then fetch promising ones " +
		"with webfetch. The query itself is sent to the search engine."
}

// Parameters implements agent.Tool.
func (t *websearchTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"query": prop("string", "what to search for"),
		"limit": prop("integer", "optional: max results (default 5, max 10)"),
	}, "query")
}

// Execute implements agent.Tool.
func (t *websearchTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return errResult("query is required")
	}
	if t.get == nil {
		return errResult("websearch not wired")
	}
	res, err := t.get().Search(ctx, a.Query, a.Limit)
	if err != nil {
		return errResult("%v", err)
	}
	var b strings.Builder
	for i, r := range res {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return agent.ToolResult{Content: truncateBytes([]byte(b.String()), websearchHead, 0)}
}

// Capability implements agent.Capabler：查询词只发往固定搜索端点（与
// webfetch 预批准文档站同类的数据出网），命中 URL 不回访——只读低风
// 险自动放行。
func (t *websearchTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
