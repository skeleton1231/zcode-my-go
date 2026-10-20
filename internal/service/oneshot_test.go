package service

// OneShot 测试（spec 规则 122 的 judge 后端）：注入回放收全量文本、
// provider 错误上浮、未配置 provider 明确报错。

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestOneShotCollectsText(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	// 缓冲要 ≥ 事件数：OneShot 在 send 全部落定后才被调用，容量不足
	// 会让 send 在读者启动前死锁（本测试的真实教训）。
	out := make(chan ai.StreamEvent, 8)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: `{"score": 5, `}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: `"comment": "ok"}`}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	svc.UseProviderForTesting(func() ai.Provider { return chProvider{ch: out} })

	text, err := svc.OneShot(context.Background(), "评委系统提示", "待评内容")
	if err != nil {
		t.Fatalf("oneshot: %v", err)
	}
	if text != `{"score": 5, "comment": "ok"}` {
		t.Fatalf("text = %q", text)
	}
}

func TestOneShotProviderError(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	out := make(chan ai.StreamEvent, 8)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError, ErrorMessage: "401 unauthorized"}
	close(out)
	svc.UseProviderForTesting(func() ai.Provider { return chProvider{ch: out} })

	if _, err := svc.OneShot(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestOneShotNoProvider(t *testing.T) {
	svc, err := New(t.TempDir()) // 无 key 无 OAuth 的干净 settings
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, err := svc.OneShot(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("err = %v", err)
	}
}
