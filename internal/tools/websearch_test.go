package tools

// websearch 工具层测试（spec 规则 121）：注入假 DDG 端点——渲染形态、
// 截断、未接线与空查询守卫。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/websearch"
)

// fakeDDGSearcher 返回指向 httptest 的后端（返回 2 条命中）。
func fakeDDGSearcher(t *testing.T) *websearch.Searcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!DOCTYPE html><body>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F">Go</a>
<a class="result__snippet">The Go site</a></div>
<div class="result"><a class="result__a" href="https://example.com/2">Second Hit</a>
<a class="result__snippet">two</a></div>
</body>`))
	}))
	t.Cleanup(srv.Close)
	return &websearch.Searcher{DDGBase: srv.URL}
}

func TestWebSearchToolExecute(t *testing.T) {
	tool := NewWebSearchTool(func() *websearch.Searcher { return fakeDDGSearcher(t) })
	res := tool.Execute(context.Background(), []byte(`{"query":"go","limit":5}`))
	if res.IsError {
		t.Fatalf("execute: %s", res.Content)
	}
	for _, want := range []string{"1. Go\n   https://go.dev/", "The Go site", "2. Second Hit\n   https://example.com/2"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("output missing %q:\n%s", want, res.Content)
		}
	}
}

// TestWebSearchToolTruncates：超长输出按 8KB 截断。
func TestWebSearchToolTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString("<html><body>")
		for i := 0; i < 10; i++ {
			b.WriteString(`<div class="result"><a class="result__a" href="https://example.com/p` +
				strconv.Itoa(i) + `">` + strings.Repeat("长标题", 900) + `</a></div>`)
		}
		b.WriteString("</body></html>")
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()
	tool := NewWebSearchTool(func() *websearch.Searcher { return &websearch.Searcher{DDGBase: srv.URL} })
	res := tool.Execute(context.Background(), []byte(`{"query":"x"}`))
	if res.IsError {
		t.Fatalf("execute: %s", res.Content)
	}
	// 家族口径：截断可带一行省略提示，正文不得超上限。
	if !strings.Contains(res.Content, "truncated") {
		t.Fatalf("no truncation notice:\n%.200s", res.Content)
	}
	if len(res.Content) > websearchHead+64 {
		t.Fatalf("output %d bytes > cap %d + notice", len(res.Content), websearchHead)
	}
}

// TestWebSearchToolGuards：空查询/未接线。
func TestWebSearchToolGuards(t *testing.T) {
	wired := NewWebSearchTool(func() *websearch.Searcher { return fakeDDGSearcher(t) })
	res := wired.Execute(context.Background(), []byte(`{"query":"  "}`))
	if !res.IsError || !strings.Contains(res.Content, "query is required") {
		t.Fatalf("empty query: %+v", res)
	}
	res = NewWebSearchTool(nil).Execute(context.Background(), []byte(`{"query":"x"}`))
	if !res.IsError || !strings.Contains(res.Content, "not wired") {
		t.Fatalf("unwired: %+v", res)
	}
}

// TestWebSearchToolCapability：只读低风险（固定端点出网、不回访 URL）。
func TestWebSearchToolCapability(t *testing.T) {
	c := NewWebSearchTool(nil).(*websearchTool).Capability()
	if !c.ReadOnly || c.Risk != permission.RiskLow {
		t.Fatalf("capability = %+v", c)
	}
}
