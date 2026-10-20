package ai

// 模型目录在线刷新（spec 规则 125）：内置目录是离线快照——新模型/
// 自建端点（vLLM 等）不在其中，上下文按默认猜。FetchModelList 按协议
// 拉各家的模型清单 API；结果作为持久化覆盖层（dataDir/models.json）
// 参与查询：内置条目带牌价/窗口——同名内置优先，覆盖层只补未知 id。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// refreshTimeout / refreshMaxModels：清单拉取的上限（运维命令不该挂
// 死；巨清单截断——目录是建议面不是账本）。
const (
	refreshTimeout  = 15 * time.Second
	refreshMaxModel = 500
)

// FetchModelList 按协议拉模型清单（zgo models --refresh 的执行面）。
// URL 拼法与各 provider 的请求约定一致：anthropic/gemini 的 BaseURL 是
// API 根（拼 /v1、/v1beta），openai 的 BaseURL 含 /v1（直接拼 /models）。
// 返回条目只有 ID/API——上下文/输出/牌价未知（0 = 未知，成本与窗口
// 面按默认估算，不瞎猜）。
func FetchModelList(ctx context.Context, api, baseURL, apiKey string, client *http.Client) ([]ModelInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: refreshTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	var (
		url     string
		headers map[string]string
		parse   func([]byte) ([]ModelInfo, error)
	)
	switch api {
	case "", "anthropic":
		url = strings.TrimRight(baseURL, "/") + "/v1/models"
		headers = map[string]string{"x-api-key": apiKey, "anthropic-version": anthropicVersion}
		if apiKey == "" { // OAuth 订阅形态
			headers = map[string]string{"anthropic-version": anthropicVersion}
		}
		parse = parseAnthropicModelList
	case "openai":
		url = strings.TrimRight(baseURL, "/") + "/models"
		headers = map[string]string{"Authorization": "Bearer " + apiKey}
		parse = parseOpenAIModelList
	case "gemini":
		url = strings.TrimRight(baseURL, "/") + "/v1beta/models"
		headers = map[string]string{"x-goog-api-key": apiKey}
		parse = parseGeminiModelList
	default:
		return nil, fmt.Errorf("models: 未知协议 %q", api)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models: %s 状态 %d", req.URL.Host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	out, err := parse(body)
	if err != nil {
		return nil, err
	}
	if len(out) > refreshMaxModel {
		out = out[:refreshMaxModel]
	}
	return out, nil
}

func parseAnthropicModelList(body []byte) ([]ModelInfo, error) {
	var payload struct {
		Data []struct {
			Name string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("models: 响应解析失败: %w", err)
	}
	var out []ModelInfo
	for _, d := range payload.Data {
		if d.Name == "" {
			continue
		}
		out = append(out, ModelInfo{ID: d.Name, API: "anthropic"})
	}
	return out, nil
}

func parseOpenAIModelList(body []byte) ([]ModelInfo, error) {
	var payload struct {
		Data []struct {
			Name string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("models: 响应解析失败: %w", err)
	}
	var out []ModelInfo
	for _, d := range payload.Data {
		if d.Name == "" {
			continue
		}
		out = append(out, ModelInfo{ID: d.Name, API: "openai"})
	}
	return out, nil
}

func parseGeminiModelList(body []byte) ([]ModelInfo, error) {
	var payload struct {
		Models []struct {
			Name                 string   `json:"name"`
			SupportedGenerations []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("models: 响应解析失败: %w", err)
	}
	var out []ModelInfo
	for _, m := range payload.Models {
		// 只收支持 generateContent 的条目（embedding/tts 等不进目录）。
		generatable := false
		for _, g := range m.SupportedGenerations {
			if g == "generateContent" {
				generatable = true
				break
			}
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if id == "" || m.Name == id || !generatable { // 无 models/ 前缀 = 非生成条目
			continue
		}
		out = append(out, ModelInfo{ID: id, API: "gemini"})
	}
	return out, nil
}

// ---- 覆盖层（持久化的刷新结果）----

var (
	overlayMu sync.RWMutex
	overlay   []ModelInfo // 已归一化（保序；查重靠线性——≤500 条无压力）
)

// ApplyCatalogOverlay 替换内存覆盖层（服务启动载入 / 刷新后更新）。
func ApplyCatalogOverlay(entries []ModelInfo) {
	overlayMu.Lock()
	defer overlayMu.Unlock()
	overlay = entries
}

// CatalogOverlay 返回覆盖层副本（models CLI 展示用）。
func CatalogOverlay() []ModelInfo {
	overlayMu.RLock()
	defer overlayMu.RUnlock()
	return append([]ModelInfo(nil), overlay...)
}

// overlayLookup 在覆盖层里查 id（精确 → 家族前缀，与内置目录同语义）。
// 返回零值 ModelInfo{} 表示不在覆盖层。
func overlayLookup(id string) (ModelInfo, bool) {
	overlayMu.RLock()
	defer overlayMu.RUnlock()
	lower := strings.ToLower(id)
	var best ModelInfo
	for _, m := range overlay {
		if strings.EqualFold(m.ID, id) {
			return overlayDefaults(m), true
		}
		if strings.HasPrefix(lower, strings.ToLower(m.ID)) && len(m.ID) > len(best.ID) {
			best = m
		}
	}
	if best.ID != "" {
		return overlayDefaults(best), true
	}
	return ModelInfo{}, false
}

// overlayDefaults 给覆盖层条目补默认窗口/输出（拉清单 API 不带这些
// 元数据；0=未知会让窗口面瞎猜——补齐默认值与内置 fallback 同口径）。
func overlayDefaults(m ModelInfo) ModelInfo {
	m.ContextWindow = DefaultContextWindow
	m.MaxOutput = DefaultMaxOutput
	return m
}

// overlayKnown 报告 id 是否命中覆盖层（含前缀）。
func overlayKnown(id string) bool {
	_, ok := overlayLookup(id)
	return ok
}
