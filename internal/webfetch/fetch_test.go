package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCheckAddrEgressBlockedRanges(t *testing.T) {
	blocked := []string{
		// IPv4 私网/特殊段。
		"127.0.0.1", "10.0.0.1", "172.16.0.1", "172.31.255.255",
		"192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "198.18.0.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"192.0.2.1", "203.0.113.7",
		// IPv6 环回/ULA/链路本地/组播/文档/6to4。
		"::1", "::", "fd00::1", "fe80::1", "ff02::1", "2001:db8::1",
		"2002:101:101::1",
		// 4in6 与 NAT64 内嵌私网 IPv4——还原后同样拒绝。
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "64:ff9b::a00:1",
	}
	for _, s := range blocked {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("parse %s: %v", s, err)
		}
		if err := CheckAddrEgress(addr); err == nil {
			t.Errorf("%s not blocked", s)
		}
	}
	public := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", "172.32.0.1", // 172.32 在 /12 之外
		"2606:4700:4700::1111", "2a00:1450:4001::81",
		"::ffff:8.8.8.8", "64:ff9b::808:808", // 公网 IPv4 的映射/NAT64 形态
	}
	for _, s := range public {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("parse %s: %v", s, err)
		}
		if err := CheckAddrEgress(addr); err != nil {
			t.Errorf("%s wrongly blocked: %v", s, err)
		}
	}
}

func TestCheckHostEgressLocalhostAndDomains(t *testing.T) {
	for _, h := range []string{"localhost", "LOCALHOST", "app.localhost", "192.168.0.1", "[10.0.0.1]"} {
		if err := CheckHostEgress(h); err == nil {
			t.Errorf("%s not blocked", h)
		}
	}
	// 普通域名字面量层放行（不做 DNS 预解析——取舍见 egress.go）。
	if err := CheckHostEgress("example.com"); err != nil {
		t.Errorf("domain blocked: %v", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	bad := []string{
		"", "not a url", "ftp://example.com", "file:///etc/passwd",
		"http://user:pass@example.com", "https://localhost/x",
		"http://intranet-host/", "http://10.0.0.1/", "https://[fd00::1]/",
		strings.Repeat("https://a.example/", 200),
	}
	for _, raw := range bad {
		if _, err := NormalizeURL(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
	u, err := NormalizeURL("http://Example.COM:80/docs")
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "Example.COM" {
		t.Fatalf("http→https upgrade / default port strip wrong: %s", u)
	}
	u, err = NormalizeURL("https://example.com/path?q=1")
	if err != nil || u.Host != "example.com" {
		t.Fatalf("good URL rejected: %v %v", u, err)
	}
}

func TestExtractContentHTML(t *testing.T) {
	html := `<!-- c --><script>bad()</script><style>.x{}</style>
<html><body><h1>标题</h1><p>第一段 &amp; 实体</p>
<ul><li>项一</li><li>项二</li></ul>
<a href="/next">下一页</a></body></html>`
	text, err := ExtractContent([]byte(html), "text/html; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 标题", "第一段 & 实体", "- 项一", "- 项二", "[下一页](/next)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "bad()") || strings.Contains(text, ".x{}") {
		t.Errorf("script/style leaked: %s", text)
	}
}

func TestExtractContentNonText(t *testing.T) {
	if _, err := ExtractContent([]byte("x"), "image/png"); err == nil {
		t.Fatal("binary accepted")
	}
	if _, err := ExtractContent([]byte(`{"a":1}`), "application/json"); err != nil {
		t.Fatalf("json rejected: %v", err)
	}
}

func TestTruncateForModel(t *testing.T) {
	s := strings.Repeat("字", MaxModelChars+50)
	out, truncated := TruncateForModel(s)
	if !truncated || len([]rune(out)) > MaxModelChars {
		t.Fatalf("truncation wrong: len=%d truncated=%v", len([]rune(out)), truncated)
	}
	if out, truncated := TruncateForModel("短"); truncated || out != "短" {
		t.Fatal("short content truncated")
	}
}

// testFetcher 搭一个对 httptest（127.0.0.1 + http）旁路归一与 egress 的
// Fetcher——被测的是 fetch 管线本身，不是守卫（守卫有专测）。
func testFetcher(t *testing.T, h http.HandlerFunc) (*Fetcher, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := &http.Client{
		Timeout: Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Fetcher{
		Client:    client,
		Cache:     NewCache(),
		Normalize: url.Parse,
		Guard:     func(string) error { return nil },
	}, srv
}

func TestFetchHappyPathAndCache(t *testing.T) {
	hits := 0
	f, srv := testFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Doc</h1><p>正文</p>"))
	})
	res, err := f.Fetch(context.Background(), srv.URL+"/a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "# Doc") || !strings.Contains(res.Content, "正文") {
		t.Fatalf("content wrong: %q", res.Content)
	}
	res2, err := f.Fetch(context.Background(), srv.URL+"/a")
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 || !res2.CacheHit {
		t.Fatalf("cache miss: hits=%d cacheHit=%v", hits, res2.CacheHit)
	}
}

func TestFetchSameSiteRedirectFollows(t *testing.T) {
	var base string
	f, srv := testFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			w.Header().Set("Location", base+"/b")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("landed"))
	})
	base = srv.URL
	res, err := f.Fetch(context.Background(), srv.URL+"/a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "landed") || len(res.Redirects) != 1 {
		t.Fatalf("same-site redirect not followed: %+v", res)
	}
	if res.FinalURL != srv.URL+"/b" {
		t.Fatalf("final URL wrong: %s", res.FinalURL)
	}
}

func TestFetchCrossSiteRedirectStops(t *testing.T) {
	f, srv := testFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			// 跨「主机」：端口不同即不同源。b 端口上没有服务，但工具
			// 必须在跟随前停下，不会真的发起连接。
			w.Header().Set("Location", "http://127.0.0.1:1/b")
			w.WriteHeader(302)
			return
		}
		w.Write([]byte("nope"))
	})
	res, err := f.Fetch(context.Background(), srv.URL+"/a")
	if err != nil {
		t.Fatalf("cross-site stop reported as error: %v", err)
	}
	if !strings.Contains(res.Content, "重定向到其他主机") || len(res.Redirects) != 1 {
		t.Fatalf("cross-site report wrong: %+v", res)
	}
}

func TestFetchHTTPErrorCarriesSnippet(t *testing.T) {
	f, srv := testFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing thing", http.StatusNotFound)
	})
	_, err := f.Fetch(context.Background(), srv.URL+"/a")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "missing thing") {
		t.Fatalf("error snippet lost: %v", err)
	}
}

func TestFetchRedirectLoopCap(t *testing.T) {
	var base string
	f, srv := testFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", base+"/a")
		w.WriteHeader(302)
	})
	base = srv.URL
	if _, err := f.Fetch(context.Background(), srv.URL+"/a"); err == nil ||
		!strings.Contains(err.Error(), "重定向") {
		t.Fatalf("loop not capped: %v", err)
	}
}

func TestCacheTTLAndCap(t *testing.T) {
	c := NewCache()
	c.Put("k", CacheEntry{Content: "v", StoredAt: time.Now()})
	if _, ok := c.Get("k"); !ok {
		t.Fatal("fresh entry missing")
	}
	c.Put("k", CacheEntry{Content: "v", StoredAt: time.Now().Add(-2 * CacheTTL)})
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired entry survived")
	}
}

func TestIsPreapprovedURL(t *testing.T) {
	yes := []string{
		"https://go.dev/pkg/netip", "https://developer.mozilla.org/en-US/docs/Web",
		"https://huggingface.co/docs/transformers", "https://vercel.com/docs/x/y",
	}
	no := []string{
		"https://evil.com", "https://go.dev.evil.com/",
		"https://huggingface.co/blog/x",          // 前缀外路径
		"https://vercel.com/docs%2f..%2faccount", // 编码斜杠绕过
		"https://huggingface.co/%252fdocs",       // 双重编码
		"not a url",
	}
	for _, u := range yes {
		if !IsPreapprovedURL(u) {
			t.Errorf("%s not preapproved", u)
		}
	}
	for _, u := range no {
		if IsPreapprovedURL(u) {
			t.Errorf("%s wrongly preapproved", u)
		}
	}
}
