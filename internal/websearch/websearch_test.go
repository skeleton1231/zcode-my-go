package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ddgFixture 是 html 端点的最小形态：1 个广告位 + 2 个真结果（uddg
// 跳转包装、摘录带标签与实体）。
const ddgFixture = `<!DOCTYPE html><html><body>
<div class="result result--ad">
  <a class="result__a" href="//duckduckgo.com/y.js?ad_domain=ads.example">Buy Now — 广告</a>
  <a class="result__snippet">sponsored</a>
</div>
<div class="result results_links">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdocs%2F&amp;rut=abc">Go <b>Documentation</b> &amp; Tutorials</a>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdocs%2F">The official &lt;site&gt; for the Go language.</a>
</div>
<div class="result results_links">
  <a rel="nofollow" class="result__a" href="https://example.com/go-book">Example Go Book</a>
  <a class="result__snippet">A practical <em>book</em> about Go.</a>
</div>
</body></html>`

// TestSearchDDGParsesResults：POST 表单带查询词；广告跳过；uddg 解
// 包；标题/摘录剥标签折空白。
func TestSearchDDGParsesResults(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		gotQuery = r.Form.Get("q")
		_, _ = w.Write([]byte(ddgFixture))
	}))
	defer srv.Close()
	s := &Searcher{DDGBase: srv.URL}
	res, err := s.Search(context.Background(), "go docs", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotQuery != "go docs" {
		t.Fatalf("query sent = %q", gotQuery)
	}
	if len(res) != 2 {
		t.Fatalf("results = %+v（广告应被过滤）", res)
	}
	if res[0].URL != "https://go.dev/docs/" {
		t.Fatalf("uddg unwrap: %q", res[0].URL)
	}
	if res[0].Title != "Go Documentation & Tutorials" {
		t.Fatalf("title clean: %q", res[0].Title)
	}
	if res[0].Snippet != "The official <site> for the Go language." {
		t.Fatalf("snippet clean: %q", res[0].Snippet)
	}
	if res[1].URL != "https://example.com/go-book" {
		t.Fatalf("direct link: %q", res[1].URL)
	}
}

// TestSearchDDGLimit：limit 截停在第 1 条真结果。
func TestSearchDDGLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ddgFixture))
	}))
	defer srv.Close()
	res, err := (&Searcher{DDGBase: srv.URL}).Search(context.Background(), "go", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("limit not applied: %+v", res)
	}
}

// TestSearchDDGNoResults：无结果块的页面按「no results」错误呈现。
func TestSearchDDGNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>没有结果</body></html>"))
	}))
	defer srv.Close()
	_, err := (&Searcher{DDGBase: srv.URL}).Search(context.Background(), "zzz", 5)
	if err == nil || !strings.Contains(err.Error(), "no results") {
		t.Fatalf("err = %v", err)
	}
}

// TestSearchBraveParsesResults：鉴权头与 count 参数送达；JSON 命中解
// 析。
func TestSearchBraveParsesResults(t *testing.T) {
	var gotToken, gotCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Subscription-Token")
		gotCount = r.URL.Query().Get("count")
		_, _ = w.Write([]byte(`{"web":{"results":[
			{"title":"Go <b>Blog</b>","url":"https://go.dev/blog/","description":"The Go &amp; blog"},
			{"title":"","url":"","description":"空 URL 条目跳过"},
			{"title":"Second","url":"https://example.org/2","description":"two"}
		]}}`))
	}))
	defer srv.Close()
	s := &Searcher{Provider: "brave", BraveKey: "test-token", BraveBase: srv.URL}
	res, err := s.Search(context.Background(), "go blog", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotToken != "test-token" {
		t.Fatalf("token header = %q", gotToken)
	}
	if gotCount != "5" {
		t.Fatalf("count = %q", gotCount)
	}
	if len(res) != 2 || res[0].Title != "Go Blog" || res[0].Snippet != "The Go & blog" {
		t.Fatalf("results = %+v", res)
	}
}

// TestSearchValidation：空查询/超长查询/未知引擎/brave 缺 key。
func TestSearchValidation(t *testing.T) {
	s := &Searcher{}
	if _, err := s.Search(context.Background(), "   ", 5); err == nil {
		t.Fatal("empty query accepted")
	}
	if _, err := s.Search(context.Background(), strings.Repeat("x", MaxQueryBytes+1), 5); err == nil {
		t.Fatal("long query accepted")
	}
	if _, err := (&Searcher{Provider: "bogus"}).Search(context.Background(), "x", 5); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := (&Searcher{Provider: "brave"}).Search(context.Background(), "x", 5); err == nil ||
		!strings.Contains(err.Error(), "braveKey") {
		t.Fatalf("brave without key: %v", err)
	}
}

// TestSearchLimitClamp：limit<1 回落默认、>Max 夹顶。
func TestSearchLimitClamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ddgFixture))
	}))
	defer srv.Close()
	for _, tc := range []struct {
		in, want int
	}{{0, DefaultLimit}, {-3, DefaultLimit}, {99, MaxLimit}, {2, 2}} {
		if got := clampLimit(tc.in); got != tc.want {
			t.Fatalf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestSearchHTTPError：非 200 按可读错误呈现。
func TestSearchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := (&Searcher{DDGBase: srv.URL}).Search(context.Background(), "x", 5)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}
