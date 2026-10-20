// Package websearch implements the websearch tool's backend（CC 工具族
// 的 web 搜索半边，spec 规则 121）：固定端点出网——默认 DuckDuckGo
// html 端点（无 key），settings.search 可切 Brave API（key 与 apiKey
// 同一条脱敏边界）。结果只回标题/URL/摘录文本，不回访命中 URL——查
// 询词之外的输入不产生任意出网点（无 SSRF 面；深挖走 webfetch，那边
// 有 egress 守卫与权限询问）。
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 量纲：单次搜索上限、命中条数与查询词长度（工具层另有 8KB 输出截
// 断）。搜索是回合内的辅助信息源，不宜久等也不宜刷屏。
const (
	SearchTimeout = 15 * time.Second
	DefaultLimit  = 5
	MaxLimit      = 10
	MaxQueryBytes = 400
)

// 端点（Searcher 可覆写——测试接 httptest）。
const (
	DDGEndpoint   = "https://html.duckduckgo.com/html/"
	BraveEndpoint = "https://api.search.brave.com/res/v1/web/search"
)

// UserAgent 标识自身（站点可识别；与 webfetch 同款约定）。
const UserAgent = "Zgo-WebSearch/0.1"

// Result 一条搜索命中。
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Searcher 是搜索后端：Provider 空/ddg 走 DDG（无 key）；brave 需要
// BraveKey。Client 为 nil 时用进程级共享客户端（连接池跨会话）。
type Searcher struct {
	Provider string
	BraveKey string
	Client   *http.Client
	// DDGBase/BraveBase 是端点覆写（测试缝；空即默认端点）。
	DDGBase   string
	BraveBase string
}

func (s *Searcher) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return SharedClient()
}

func (s *Searcher) ddgBase() string {
	if s.DDGBase != "" {
		return s.DDGBase
	}
	return DDGEndpoint
}

func (s *Searcher) braveBase() string {
	if s.BraveBase != "" {
		return s.BraveBase
	}
	return BraveEndpoint
}

// Search 执行搜索：query 去空白（空即错）、限长；limit 非法回落默认
// 值并夹到上限。无命中按错误呈现（调用方转成可读输出——模型可换词
// 重搜）。
func (s *Searcher) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("websearch: empty query")
	}
	if len(q) > MaxQueryBytes {
		return nil, fmt.Errorf("websearch: query too long (max %d bytes)", MaxQueryBytes)
	}
	limit = clampLimit(limit)
	ctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()
	switch s.Provider {
	case "", "ddg":
		return s.searchDDG(ctx, q, limit)
	case "brave":
		if s.BraveKey == "" {
			return nil, errors.New("websearch: provider brave 需要 settings.search.braveKey")
		}
		return s.searchBrave(ctx, q, limit)
	default:
		return nil, fmt.Errorf("websearch: unknown provider %q", s.Provider)
	}
}

// clampLimit 把 limit 夹到 [1, MaxLimit]（<1 回落默认值）。
func clampLimit(limit int) int {
	if limit < 1 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

// searchDDG POST 表单到 html 端点（GET 也通，POST 形态更稳且免 URL
// 长度顾虑）。
func (s *Searcher) searchDDG(ctx context.Context, query string, limit int) ([]Result, error) {
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ddgBase(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", UserAgent)
	return s.doParse(ctx, req, func(body []byte) ([]Result, error) {
		return parseDDG(string(body), limit)
	})
}

// searchBrave GET Brave Web Search API（X-Subscription-Token 鉴权）。
func (s *Searcher) searchBrave(ctx context.Context, query string, limit int) ([]Result, error) {
	u := s.braveBase() + "?q=" + url.QueryEscape(query) + "&count=" + strconv.Itoa(limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", s.BraveKey)
	req.Header.Set("User-Agent", UserAgent)
	return s.doParse(ctx, req, func(body []byte) ([]Result, error) {
		return parseBrave(body, limit)
	})
}

// doParse 发请求、判状态、限读 2MB，再交给引擎各自的解析器。
func (s *Searcher) doParse(ctx context.Context, req *http.Request, parse func([]byte) ([]Result, error)) ([]Result, error) {
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("websearch: %s 状态 %d", req.URL.Host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return parse(body)
}

// DDG html 端点的解析口径：结果块按 class 含独立词 "result" 的容器
// 切分（result__a 之类带下划线的类名不命中 \bresult\b），块内取标题
// 锚点与摘录锚点；广告位（result--ad）与无法解包的跳转链接跳过。
// markup 变更时最多解析为空——按「无结果」如实呈现，不猜。
var (
	ddgBlockRe   = regexp.MustCompile(`(?i)<div[^>]*class="[^"]*\bresult\b[^"]*"`)
	ddgLinkRe    = regexp.MustCompile(`<a[^>]*class="[^"]*\bresult__a\b[^"]*"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*\bresult__snippet\b[^"]*"[^>]*>(.*?)</a>`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	spaceRe      = regexp.MustCompile(`\s+`)
)

// parseDDG 从结果块序列抽取命中（每块至多一条：标题 + 摘录同块配
// 对，避免跨块错位）。块的容器标签是切分分隔符——广告判定
// （result--ad）要看分隔符本身，不能看块内容。
func parseDDG(body string, limit int) ([]Result, error) {
	var out []Result
	matches := ddgBlockRe.FindAllStringIndex(body, -1)
	for i, loc := range matches {
		if len(out) >= limit {
			break
		}
		container := body[loc[0]:loc[1]] // <div class="result [result--ad] …">
		if strings.Contains(container, "result--ad") {
			continue
		}
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := body[loc[1]:end]
		m := ddgLinkRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		target, err := unwrapDDGLink(html.UnescapeString(m[1]))
		if err != nil {
			continue
		}
		r := Result{Title: cleanText(m[2]), URL: target}
		if sn := ddgSnippetRe.FindStringSubmatch(block); sn != nil {
			r.Snippet = cleanText(sn[1])
		}
		if r.Title == "" && r.URL == "" {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("websearch: no results")
	}
	return out, nil
}

// unwrapDDGLink 解包 DDG 的跳转包装（//duckduckgo.com/l/?uddg=<编码
// 目标>）；已是目标 URL（绝对或协议相对）则归一成可读形态。
func unwrapDDGLink(href string) (string, error) {
	h := strings.TrimSpace(href)
	if strings.HasPrefix(h, "//") {
		h = "https:" + h
	}
	u, err := url.Parse(h)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("websearch: bad result link %q", href)
	}
	if strings.EqualFold(u.Host, "duckduckgo.com") && u.Path == "/l/" {
		target := u.Query().Get("uddg")
		if target == "" {
			return "", errors.New("websearch: redirect wrapper without uddg")
		}
		return target, nil // Query().Get 已解码
	}
	return u.String(), nil
}

// parseBrave 解析 Brave Web Search 响应（web.results[]）。
func parseBrave(body []byte, limit int) ([]Result, error) {
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: brave 响应解析失败: %w", err)
	}
	var out []Result
	for _, r := range payload.Web.Results {
		if len(out) >= limit {
			break
		}
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, Result{Title: cleanText(r.Title), URL: r.URL, Snippet: cleanText(r.Description)})
	}
	if len(out) == 0 {
		return nil, errors.New("websearch: no results")
	}
	return out, nil
}

// cleanText 把 HTML 片段变成单行文本：先剥真实标签，再解实体——顺
// 序不能反（&lt;site&gt; 解开成 <site> 会被当标签剥掉，字面文本就丢
// 了），最后折叠空白。
func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

var (
	sharedClientOnce sync.Once
	sharedClient     *http.Client
)

// SharedClient 是进程级共享客户端（连接池跨会话/表面，同 webfetch 的
// Fetcher 约定）。
func SharedClient() *http.Client {
	sharedClientOnce.Do(func() {
		sharedClient = &http.Client{Timeout: SearchTimeout}
	})
	return sharedClient
}
