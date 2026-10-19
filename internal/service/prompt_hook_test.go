package service

// 提示钩子三态（spec 规则 96）：settings.hooks.userPromptSubmit 注入
// 后——拦截（明确报错不进转录）、改写（替换文进转录）、无钩子路径
// 不变。helper = 测试二进制自重执行（ZGO_HOOK_MODE 决定行为）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestHookPromptSvcHelper(t *testing.T) {
	switch os.Getenv("ZGO_HOOK_MODE") {
	case "svc-prompt-block":
		os.Stderr.WriteString("含敏感词")
		os.Exit(2)
	case "svc-prompt-rewrite":
		var p map[string]any
		json.NewDecoder(os.Stdin).Decode(&p)
		if !strings.HasPrefix(p["prompt"].(string), "原文：") {
			os.Stderr.WriteString("钩子看到的不是原始提示")
			os.Exit(3)
		}
		os.Stdout.WriteString(`{"prompt":"改写后的提示"}`)
		os.Exit(0)
	default:
		return
	}
}

type plainProvider struct {
	mu   sync.Mutex
	last string
}

func (p *plainProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu.Lock()
	p.last = req.Messages[len(req.Messages)-1].Text()
	p.mu.Unlock()
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "ok"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func promptHookSvc(t *testing.T, mode string) (*AgentService, string, *plainProvider) {
	t.Helper()
	dir := t.TempDir()
	hooks := map[string]any{}
	if mode != "" {
		cmd := os.Args[0] + " -test.run=TestHookPromptSvcHelper"
		hooks = map[string]any{"userPromptSubmit": []map[string]any{{"command": cmd}}}
		os.Setenv("ZGO_HOOK_MODE", mode)
		t.Cleanup(func() { os.Unsetenv("ZGO_HOOK_MODE") })
	}
	raw, _ := json.Marshal(map[string]any{"hooks": hooks})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &plainProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	return svc, info.ID, p
}

func TestPromptHookBlocksAndRewrites(t *testing.T) {
	// 拦截：明确报错、不进转录。
	svc, id, _ := promptHookSvc(t, "svc-prompt-block")
	err := svc.Chat(id, "原文：看看", func(agent.Event) {})
	if err == nil || !strings.Contains(err.Error(), "钩子拦截") || !strings.Contains(err.Error(), "含敏感词") {
		t.Fatalf("block err = %v", err)
	}
	if msgs, _ := svc.Messages(id); len(msgs) != 0 {
		t.Fatalf("blocked prompt leaked into transcript: %d", len(msgs))
	}

	// 改写：钩子看到原始提示（断言在 helper 里），替换文进模型请求。
	svc2, id2, p := promptHookSvc(t, "svc-prompt-rewrite")
	if err := svc2.Chat(id2, "原文：修一下", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	last := p.last
	p.mu.Unlock()
	if last != "改写后的提示" {
		t.Fatalf("model saw %q", last)
	}

	// 无钩子：原文直通。
	svc3, id3, p3 := promptHookSvc(t, "")
	if err := svc3.Chat(id3, "原文：直通", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	p3.mu.Lock()
	last3 := p3.last
	p3.mu.Unlock()
	if last3 != "原文：直通" {
		t.Fatalf("no-hook path altered prompt: %q", last3)
	}
}
