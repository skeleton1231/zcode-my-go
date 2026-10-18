package mcp

// Streamable HTTP 传输（2025-03 MCP 规范，spec 规则 69）：每条 JSON-RPC
// 消息 POST 到 endpoint；响应按 Content-Type 分流——application/json
// 直读，text/event-stream 解析 SSE 事件流取首个响应。会话：initialize
// 响应的 Mcp-Session-Id 头记存并回传；404 判会话失效——清会话重握手
// 并重试原请求一次（断线重连不重启应用）。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// httpTransport is the HTTP wire.
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	mu         sync.Mutex
	session    string
	handshake  func(ctx context.Context) error // 重连后的再握手（由 Server 注入）
	reconnOnce bool                             // 防嵌套重连
}

// newHTTPTransport builds the transport; handshake is attached later by
// the server start path (Start 在 transport 之上编排握手，重连复用).
func newHTTPTransport(cfg Config) (transport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp: empty url")
	}
	return &httpTransport{url: cfg.URL, headers: cfg.Headers,
		client: &http.Client{}}, nil
}

func (t *httpTransport) close() {}

// roundTrip POSTs one message. notify 期望 202/无响应体。
func (t *httpTransport) roundTrip(ctx context.Context, body []byte, notify bool) (json.RawMessage, error) {
	raw, err := t.post(ctx, body, notify)
	if err == nil {
		return raw, nil
	}
	// 会话失效（规范：404 + 已有会话）：清会话、重握手、重试一次。
	if !notify && t.isSessionLost(err) {
		t.mu.Lock()
		had := t.session != ""
		t.session = ""
		handshake := t.handshake
		reconnectBusy := t.reconnOnce
		t.reconnOnce = true
		t.mu.Unlock()
		if !had || handshake == nil || reconnectBusy {
			return nil, err
		}
		defer func() {
			t.mu.Lock()
			t.reconnOnce = false
			t.mu.Unlock()
		}()
		if herr := handshake(ctx); herr != nil {
			return nil, fmt.Errorf("reconnect: %w", herr)
		}
		return t.post(ctx, body, notify)
	}
	return nil, err
}

// sessionLostError 标记 404（会话失效判定依据）。
type sessionLostError struct{ status int }

func (e *sessionLostError) Error() string {
	return fmt.Sprintf("mcp http: session lost (HTTP %d)", e.status)
}

func (t *httpTransport) isSessionLost(err error) bool {
	lost, ok := err.(*sessionLostError)
	return ok && lost.status == http.StatusNotFound
}

// post 发一次 POST 并取回响应体（notify 为 nil 响应）。
func (t *httpTransport) post(ctx context.Context, body []byte, notify bool) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	session := t.session
	t.mu.Unlock()
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v) // 鉴权头只进请求，永不进错误/日志
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http: %w", err)
	}
	defer resp.Body.Close()

	if newSession := resp.Header.Get("Mcp-Session-Id"); newSession != "" {
		t.mu.Lock()
		t.session = newSession
		t.mu.Unlock()
	}
	if notify {
		// 通知形态：202 或 200 即完成（无响应体要读）。
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("mcp http: notification: HTTP %d", resp.StatusCode)
		}
		return nil, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, &sessionLostError{status: resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp http: HTTP %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "text/event-stream"):
		return firstSSEData(resp.Body)
	default: // application/json（及未标注的宽容路径）
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, fmt.Errorf("mcp http: reading response: %w", err)
		}
		return json.RawMessage(data), nil
	}
}

// firstSSEData 解析 SSE 事件流：跳过注释/心跳/通知，返回首个「响应」
//（有 id 无 method 的 data 载荷）；流提前结束报错。
func firstSSEData(r io.Reader) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "" || strings.HasPrefix(line, ":"):
			continue // 事件边界与注释（心跳）
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, "event:"), strings.HasPrefix(line, "id:"):
			continue
		}
		if data.Len() > 0 {
			payload := json.RawMessage(data.String())
			var head struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(payload, &head) == nil && len(head.ID) > 0 && head.Method == "" {
				return payload, nil // 响应形态：命中
			}
			data.Reset() // 通知/请求事件：继续等
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mcp http: sse: %w", err)
	}
	return nil, fmt.Errorf("mcp http: sse stream ended without a response")
}
