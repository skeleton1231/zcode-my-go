package mcp

// Streamable HTTP 传输测试（spec 规则 69，issue #204）：httptest 起假远
// 程 MCP——initialize 发会话头、tools/list、tools/call、通知 202；SSE
// 响应形态与 404 会话失效重连各有用例。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRemoteMCP 是最小 Streamable HTTP MCP 服务器：JSON 响应形态（sse
// 开关换 SSE 形态）；会话头轮换（invalidate 后旧会话 404——重连路径）。
type fakeRemoteMCP struct {
	mu         sync.Mutex
	session    int
	valid      map[string]bool
	invalidate bool
	sawAuth    string
	sawNotify  int
	sse        bool
}

func (f *fakeRemoteMCP) handler(w http.ResponseWriter, r *http.Request) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		f.mu.Lock()
		f.sawAuth = auth
		f.mu.Unlock()
	}
	sess := r.Header.Get("Mcp-Session-Id")
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Method == "initialize" {
		f.session++
		id := fmt.Sprintf("sess-%d", f.session)
		f.valid[id] = true
		w.Header().Set("Mcp-Session-Id", id)
		f.reply(w, req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		return
	}
	// 非 initialize 必须带有效会话。
	if sess == "" || !f.valid[sess] {
		w.WriteHeader(http.StatusNotFound) // 会话失效：重连路径
		return
	}
	switch req.Method {
	case "notifications/initialized":
		f.sawNotify++
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		f.reply(w, req.ID, map[string]any{"tools": []map[string]any{
			{"name": "echo", "description": "回声"},
			{"name": "add", "description": "加法"},
		}})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if raw, err := json.Marshal(req.Params); err == nil {
			json.Unmarshal(raw, &p)
		}
		f.reply(w, req.ID, map[string]any{"content": []map[string]any{
			{"type": "text", "text": "echo: " + string(p.Arguments)},
		}})
	case "prompts/list", "resources/list":
		// 不支持的可选能力：协议错误（客户端按可选静默处理）。
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"no such method"}}`, req.ID)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// reply 发 JSON 或 SSE 形态的响应。
func (f *fakeRemoteMCP) reply(w http.ResponseWriter, id int64, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, ": keepalive\n\n")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(payload)
}

func newFakeMCP(t *testing.T, sse bool) (*fakeRemoteMCP, *httptest.Server) {
	t.Helper()
	f := &fakeRemoteMCP{valid: map[string]bool{}, sse: sse}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestHTTPStartToolsAndCall(t *testing.T) {
	f, srv := newFakeMCP(t, false)
	s, err := Start(context.Background(), "remote", Config{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer secret-token"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	if tools := s.Tools(); len(tools) != 2 || tools[0].Name != "echo" {
		t.Fatalf("tools: %+v", tools)
	}
	out, err := s.Call(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil || !strings.Contains(out, `{"x":1}`) {
		t.Fatalf("call: %v %q", err, out)
	}
	// 鉴权头递达服务器；会话头在后续请求回传（fake 侧非有效会话即 404
	// ——call 成功本身证明会话被带上）。
	if f.sawAuth != "Bearer secret-token" {
		t.Fatalf("auth header: %q", f.sawAuth)
	}
	if f.sawNotify == 0 {
		t.Fatal("initialized notification missing")
	}
}

func TestHTTPSSEResponses(t *testing.T) {
	_, srv := newFakeMCP(t, true) // SSE 响应形态
	s, err := Start(context.Background(), "remote-sse", Config{URL: srv.URL})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	if len(s.Tools()) != 2 {
		t.Fatal("sse: tools listing failed")
	}
	if _, err := s.Call(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("sse call: %v", err)
	}
}

func TestHTTPReconnectOnSessionLost(t *testing.T) {
	f, srv := newFakeMCP(t, false)
	s, err := Start(context.Background(), "remote", Config{URL: srv.URL})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	// 服务器侧作废当前会话：下一次调用 404 → 客户端重握手重试。
	f.mu.Lock()
	f.valid = map[string]bool{}
	f.mu.Unlock()
	out, err := s.Call(context.Background(), "echo", json.RawMessage(`{"re":1}`))
	if err != nil || !strings.Contains(out, `"re":1`) {
		t.Fatalf("reconnect call: %v %q", err, out)
	}
	f.mu.Lock()
	newest := f.session
	f.mu.Unlock()
	if newest < 2 {
		t.Fatalf("no re-initialize happened: sessions=%d", newest)
	}
}

func TestHTTPConfigMutualExclusion(t *testing.T) {
	if err := (Config{Command: "x", URL: "http://y"}).validate(); err == nil {
		t.Fatal("both accepted")
	}
	if err := (Config{}).validate(); err == nil {
		t.Fatal("neither accepted")
	}
	if err := (Config{URL: "http://y"}).validate(); err != nil {
		t.Fatalf("url-only rejected: %v", err)
	}
}

func TestHTTPHandshakeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second) // 超过握手超时
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Start(ctx, "slow", Config{URL: srv.URL}); err == nil {
		t.Fatal("slow server accepted")
	}
}
