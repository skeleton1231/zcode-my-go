package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// titleScriptProvider：第 1 个请求是会话回合（回 turn1 文本），第 2 个
// 起是自动命名的小额补全（回 title 文本；fail2 时回错误流）。
type titleScriptProvider struct {
	mu    sync.Mutex
	reqs  int
	turn1 string
	title string
	fail2 bool
}

func (p *titleScriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu.Lock()
	p.reqs++
	n := p.reqs
	p.mu.Unlock()
	text := p.turn1
	fail := false
	if n >= 2 {
		text, fail = p.title, p.fail2
	}
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	if fail {
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError,
			ErrorMessage: "boom"}
		close(out)
		return out
	}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: text}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func (p *titleScriptProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqs
}

// waitForTitle 轮询会话标题直到期望值或超时（自动命名是回合后的后台
// goroutine）。
func waitForTitle(t *testing.T, svc *AgentService, id, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sess, ok := svc.mgr.Get(id)
		if ok && sess.Info().Title == want {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// TestAutoTitleAfterFirstTurn: 首个回合成功后，小额补全生成的标题经
// Rename 持久化（meta 行同步），第二个回合不再触发（等值守卫）。
func TestAutoTitleAfterFirstTurn(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	p := &titleScriptProvider{turn1: "已修复", title: "修复登录重定向"}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })

	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "帮我修复登录后重定向错误的问题", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	if !waitForTitle(t, svc, info.ID, "修复登录重定向") {
		t.Fatalf("title = %q, want auto title", titleOf(t, svc, info.ID))
	}

	// 第二个回合：标题已非默认值，不再发命名请求。
	before := p.requestCount()
	if err := svc.Chat(info.ID, "再检查一下", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := p.requestCount(); n != before+1 {
		t.Fatalf("extra title requests: %d -> %d", before, n)
	}
}

// TestAutoTitleSkipsRenamed: 用户手动改名优先——改名后标题不再是默认
// 截断值，等值守卫直接跳过（连命名请求都不发）。
func TestAutoTitleSkipsRenamed(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	p := &titleScriptProvider{turn1: "done", title: "模型起的名"}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })

	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RenameSession(info.ID, "我的手工标题"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "第一回合", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := titleOf(t, svc, info.ID); got != "我的手工标题" {
		t.Fatalf("renamed title overwritten: %q", got)
	}
	if n := p.requestCount(); n != 1 {
		t.Fatalf("title request sent for renamed session: %d", n)
	}
}

// TestAutoTitleFailureKeepsDefault: 命名补全失败（错误流）保留默认标题，
// 会话本身不受影响。
func TestAutoTitleFailureKeepsDefault(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	long := "这是一个特别长的首条用户消息用来验证默认截断标题的形态是否保持原样不被覆盖"
	p := &titleScriptProvider{turn1: "ok", fail2: true}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })

	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, long, func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	want := titleOf(t, svc, info.ID)
	if want == "" || want == "ok" {
		t.Fatalf("default title lost: %q", want)
	}
}

func titleOf(t *testing.T, svc *AgentService, id string) string {
	t.Helper()
	sess, ok := svc.mgr.Get(id)
	if !ok {
		t.Fatal("session missing")
	}
	return sess.Info().Title
}
