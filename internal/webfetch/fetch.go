package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 出网口径（ZCode 同款量级）：超时 60s、重定向上限 10 跳。
const (
	Timeout      = 60 * time.Second
	MaxRedirects = 10

	// UserAgent 标识自身（站点可识别）。
	UserAgent = "Zgo-WebFetch/0.1"
)

// ErrCrossSiteRedirect 报告「停在重定向链上」——跨主机跳转不自动跟
// 随，结果里带跳转链；不是失败，调用方转成可读结果。
var ErrCrossSiteRedirect = errors.New("webfetch: cross-site redirect")

// Result 是一次抓取抽取的结果。
type Result struct {
	URL         string // 请求 URL（归一后）
	FinalURL    string // 实际命中 URL（跟随同主机重定向后）
	Status      int
	ContentType string
	Bytes       int
	DurationMs  int64
	CacheHit    bool
	Redirects   []Redirect
	Content     string
	Truncated   bool
}

// Fetcher 抓取并抽取。Normalize 与 Guard 是测试缝：生产零值走
// NormalizeURL / CheckHostEgress；tools 层测试对 httptest（127.0.0.1
// + http）需要旁路两者，字段在构造时注入、执行期只读。
type Fetcher struct {
	Client    *http.Client
	Cache     *Cache
	Normalize func(raw string) (*url.URL, error)
	Guard     func(hostname string) error
}

// NewFetcher 构建生产默认：自动重定向关闭，跳转逐跳过门。
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // 手动逐跳
			},
		},
		Cache: NewCache(),
	}
}

func (f *Fetcher) normalize(raw string) (*url.URL, error) {
	if f.Normalize != nil {
		return f.Normalize(raw)
	}
	return NormalizeURL(raw)
}

func (f *Fetcher) checkEgress(hostname string) error {
	if f.Guard != nil {
		return f.Guard(hostname)
	}
	return CheckHostEgress(hostname)
}

// Fetch 抓取 raw 并抽取文本。缓存按归一 URL 键控（命中返回先前的抽取
// 结果）。
func (f *Fetcher) Fetch(ctx context.Context, raw string) (*Result, error) {
	u, err := f.normalize(raw)
	if err != nil {
		return nil, err
	}
	key := u.String()
	if f.Cache != nil {
		if e, ok := f.Cache.Get(key); ok {
			return &Result{URL: key, FinalURL: e.FinalURL, Status: e.Status,
				ContentType: e.ContentType, CacheHit: true, Content: e.Content}, nil
		}
	}
	start := time.Now()
	res, redirects, err := f.fetchFollow(ctx, u)
	if err != nil {
		if !errors.Is(err, ErrCrossSiteRedirect) {
			return nil, err
		}
		// 跨主机跳转：停在链上，报告目标让模型决定（显式发起同样过门）。
		dur := time.Since(start).Milliseconds()
		finalURL := key
		if n := len(redirects); n > 0 {
			finalURL = redirects[n-1].To
		}
		hops := make([]string, 0, len(redirects))
		for _, r := range redirects {
			hops = append(hops, fmt.Sprintf("  %d %s → %s", r.Code, r.From, r.To))
		}
		return &Result{URL: key, FinalURL: finalURL, Status: http.StatusFound,
			DurationMs: dur, Redirects: redirects,
			Content: "重定向到其他主机，未自动跟随：\n" + strings.Join(hops, "\n") +
				"\n如需访问请对最终地址直接发起 webfetch。"}, nil
	}
	dur := time.Since(start).Milliseconds()

	finalURL := key
	if n := len(redirects); n > 0 {
		finalURL = redirects[n-1].To
	}
	content, err := ExtractContent(res.body, res.contentType)
	if err != nil {
		return nil, err
	}
	content, truncated := TruncateForModel(content)
	out := &Result{URL: key, FinalURL: finalURL, Status: res.status,
		ContentType: res.contentType, Bytes: res.bytes, DurationMs: dur,
		Redirects: redirects, Content: content, Truncated: truncated}
	if f.Cache != nil {
		f.Cache.Put(key, CacheEntry{Content: content, ContentType: res.contentType,
			Status: res.status, FinalURL: finalURL, StoredAt: time.Now()})
	}
	return out, nil
}

// hopResult 是一跳的终态响应（已读体、封顶）。
type hopResult struct {
	status      int
	contentType string
	location    string
	body        []byte
	bytes       int
}

// fetchFollow 逐跳跟随同主机重定向；跨主机停在链上（ErrCrossSiteRedirect），
// 上限/坏跳转报错。每跳（含首跳）过 egress 门——重定向是绕过首跳检查
// 的经典通道。
func (f *Fetcher) fetchFollow(ctx context.Context, u *url.URL) (*hopResult, []Redirect, error) {
	var redirects []Redirect
	current := u
	for hop := 0; hop <= MaxRedirects; hop++ {
		if err := f.checkEgress(current.Hostname()); err != nil {
			return nil, nil, err
		}
		res, err := f.get(ctx, current)
		if err != nil {
			return nil, nil, err
		}
		if res.location == "" { // 终态（2xx/4xx/5xx，或无 Location 的 3xx）
			return res, redirects, nil
		}
		next, err := ResolveRedirect(res.location, current)
		if err != nil {
			return nil, nil, err
		}
		if err := CheckRedirectHop(next); err != nil {
			return nil, nil, err
		}
		redirects = append(redirects, Redirect{From: current.String(), To: next.String(), Code: res.status})
		if !SameSiteRedirect(current, next) {
			return nil, redirects, ErrCrossSiteRedirect
		}
		current = next
	}
	return nil, nil, fmt.Errorf("webfetch: 重定向超过 %d 跳", MaxRedirects)
}

// get 发起单跳 GET 并读体（10MB 封顶；错误页带短正文回给模型自纠）。
func (f *Fetcher) get(ctx context.Context, u *url.URL) (*hopResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("webfetch: 构造请求失败 %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webfetch: 请求失败 %w", err)
	}
	defer res.Body.Close()

	r := io.LimitReader(res.Body, MaxResponseBytes+1)
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("webfetch: 读取响应失败 %w", err)
	}
	if len(body) > MaxResponseBytes {
		return nil, fmt.Errorf("webfetch: 响应超过 %d 字节上限", MaxResponseBytes)
	}
	if res.StatusCode >= 400 {
		// 错误页也带短正文回来——模型可见才能自纠（封 4KB）。
		snippet := string(body)
		if runes := []rune(snippet); len(runes) > 4096 {
			snippet = string(runes[:4096])
		}
		return nil, fmt.Errorf("webfetch: HTTP %s\n%s", res.Status, snippet)
	}
	return &hopResult{status: res.StatusCode, contentType: res.Header.Get("Content-Type"),
		location: res.Header.Get("Location"), body: body, bytes: len(body)}, nil
}
