package ai

// 模型目录在线刷新测试（spec 规则 125）：三协议清单拉取（URL/鉴权头/
// 解析/过滤）、覆盖层查询与优先级（内置优先、前缀匹配、状态复位）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resetOverlay 清理包级覆盖层（防串扰其他目录测试）。
func resetOverlay(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { ApplyCatalogOverlay(nil) })
}

func TestFetchModelListAnthropic(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-6"},{"id":"claude-sonnet-4-5"},{"id":""}]}`))
	}))
	defer srv.Close()
	list, err := FetchModelList(context.Background(), "anthropic", srv.URL, "k1", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" || gotKey != "k1" {
		t.Fatalf("path=%q key=%q", gotPath, gotKey)
	}
	if len(list) != 2 || list[0].ID != "glm-6" || list[0].API != "anthropic" {
		t.Fatalf("list = %+v", list)
	}
}

func TestFetchModelListOpenAI(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen4-max"},{"id":"deepseek-v4"}]}`))
	}))
	defer srv.Close()
	// OpenAI 惯例：BaseURL 含 /v1（与 chat/completions 同约定）。
	list, err := FetchModelList(context.Background(), "openai", srv.URL+"/v1", "k2", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" || gotAuth != "Bearer k2" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if len(list) != 2 || list[1].ID != "deepseek-v4" {
		t.Fatalf("list = %+v", list)
	}
}

func TestFetchModelListGemini(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		_, _ = w.Write([]byte(`{"models":[
			{"name":"models/gemini-4-pro","supportedGenerationMethods":["generateContent"]},
			{"name":"models/embedding-001","supportedGenerationMethods":["embedContent"]},
			{"name":"weird-entry","supportedGenerationMethods":["generateContent"]}
		]}`))
	}))
	defer srv.Close()
	list, err := FetchModelList(context.Background(), "gemini", srv.URL, "k3", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models" || gotKey != "k3" {
		t.Fatalf("path=%q key=%q", gotPath, gotKey)
	}
	// 只留 generateContent 且带 models/ 前缀的条目。
	if len(list) != 1 || list[0].ID != "gemini-4-pro" {
		t.Fatalf("list = %+v", list)
	}
}

func TestFetchModelListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := FetchModelList(context.Background(), "anthropic", srv.URL, "k", srv.Client())
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

// TestOverlayLookupPrecedence：内置优先（同名覆盖层条目不遮内置牌
// 价/窗口）；未知 id 命中覆盖层（默认口径）；家族前缀同语义。
func TestOverlayLookupPrecedence(t *testing.T) {
	resetOverlay(t)
	ApplyCatalogOverlay([]ModelInfo{
		{ID: "glm-9", API: "anthropic"},
		{ID: "claude-sonnet-4-5", API: "anthropic"}, // 与内置同名：应被内置压住
	})
	if !KnownModel("glm-9") {
		t.Fatal("overlay id unknown")
	}
	m := LookupModel("glm-9")
	if m.ContextWindow != DefaultContextWindow || m.MaxOutput != DefaultMaxOutput {
		t.Fatalf("overlay defaults: %+v", m)
	}
	// 家族前缀：覆盖层条目覆盖其日期变体。
	if !KnownModel("glm-9-20261201") {
		t.Fatal("overlay prefix not matched")
	}
	// 内置同名优先：窗口/牌价仍是内置值。
	b := LookupModel("claude-sonnet-4-5")
	if b.InPrice != 3 || b.MaxOutput != 32_000 {
		t.Fatalf("built-in shadowed by overlay: %+v", b)
	}
	if KnownModel("cloude-9") {
		t.Fatal("typo wrongly known") // 双保险：拼写错误仍应未知
	}
}
