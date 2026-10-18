package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/webfetch"
)

func TestWebFetchToolFetchesAndRenders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Docs</h1><p>正文内容</p>"))
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	f := &webfetch.Fetcher{Client: client, Cache: webfetch.NewCache(),
		Normalize: url.Parse, Guard: func(string) error { return nil }}
	tool := NewWebFetchTool(func() *webfetch.Fetcher { return f })

	res := tool.Execute(context.Background(),
		[]byte(`{"url":"`+srv.URL+`","prompt":"找出要点"}`))
	if res.IsError {
		t.Fatalf("execute failed: %s", res.Content)
	}
	for _, want := range []string{"# Docs", "正文内容", "URL: ", "状态: 200", "（按提示处理：）找出要点"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in:\n%s", want, res.Content)
		}
	}
}

func TestWebFetchToolErrors(t *testing.T) {
	f := webfetch.NewFetcher()
	tool := NewWebFetchTool(func() *webfetch.Fetcher { return f })
	if res := tool.Execute(context.Background(), []byte(`{}`)); !res.IsError {
		t.Fatal("empty url accepted")
	}
	// 私网 IP 走真实守卫：SSRF 防线经工具全链路生效。
	res := tool.Execute(context.Background(), []byte(`{"url":"https://192.168.1.1/x"}`))
	if !res.IsError || !strings.Contains(res.Content, "拒绝") {
		t.Fatalf("private IP not blocked: %+v", res)
	}
}

func TestWebFetchToolCapability(t *testing.T) {
	tool := NewWebFetchTool(func() *webfetch.Fetcher { return webfetch.NewFetcher() }).(*webfetchTool)
	if cap := tool.CapabilityForArgs(`{"url":"https://example.com/x"}`); cap.ReadOnly || cap.Risk != permission.RiskMedium {
		t.Fatalf("arbitrary host should ask: %+v", cap)
	}
	cap := tool.CapabilityForArgs(`{"url":"https://go.dev/pkg/netip"}`)
	if !cap.ReadOnly || cap.Risk != permission.RiskLow {
		t.Fatalf("preapproved host should auto-allow: %+v", cap)
	}
}
