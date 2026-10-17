package service

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	aiAuth "github.com/skeleton1231/zcode-my-go/internal/ai/auth"
)

// TestAskerSerializationSingleSlot 验证 ask 全局串行化（issue #79：跨
// 会话并发的权限确认曾覆盖单槽 modal，先到者的应答 channel 孤儿化、
// 工具 goroutine 永久挂起）。
func TestAskerSerializationSingleSlot(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	scrubEnv(t)

	var mu sync.Mutex
	var order []string
	release := make(chan struct{})
	svc.SetAsker(func(req agent.AskRequest) agent.AskResponse {
		mu.Lock()
		order = append(order, "start:"+req.Tool)
		mu.Unlock()
		<-release // 模拟单槽 modal 等待用户
		mu.Lock()
		order = append(order, "end:"+req.Tool)
		mu.Unlock()
		return agent.AskResponse{}
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); svc.ask(agent.AskRequest{Tool: "a"}) }()
	time.Sleep(20 * time.Millisecond) // 让 a 先入槽
	go func() { defer wg.Done(); svc.ask(agent.AskRequest{Tool: "b"}) }()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	frozen := append([]string(nil), order...)
	mu.Unlock()
	if len(frozen) != 1 || frozen[0] != "start:a" {
		t.Fatalf("second ask entered the single slot concurrently: %v", frozen)
	}
	close(release)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 || order[0] != "start:a" || order[1] != "end:a" ||
		order[2] != "start:b" || order[3] != "end:b" {
		t.Fatalf("order = %v, want a fully before b", order)
	}
}

// TestRefreshOAuthSingleFlight 验证并发刷新只发一次（issue #136：并行
// 子代理/DAG 同时撞过期会多路 Refresh 同一 refresh_token，轮换后其余路
// invalid_grant 回退旧 access token → 401）。
func TestRefreshOAuthSingleFlight(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	scrubEnv(t)
	// 预置已过期凭据。
	svc.mu.Lock()
	svc.settings.OAuth = aiAuth.Token{
		AccessToken:  "old",
		RefreshToken: "r1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}
	svc.mu.Unlock()

	var refreshes int32
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshes, 1)
		<-block // 拉长窗口，让并发全部进入
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","refresh_token":"r2","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	authTokenURL = srv.URL

	var wg sync.WaitGroup
	toks := make([]aiAuth.Token, 4)
	for i := range toks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			toks[i] = svc.refreshOAuth(svc.settings.OAuth)
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(block)
	wg.Wait()
	if refreshes != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshes)
	}
	for i, tk := range toks {
		if tk.AccessToken != "fresh" {
			t.Fatalf("token[%d] = %q, want fresh", i, tk.AccessToken)
		}
	}
}
