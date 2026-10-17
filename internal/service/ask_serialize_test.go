package service

import (
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
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
