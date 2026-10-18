package ai

import (
	"context"
	"strings"
	"testing"
)

// scriptedProvider 按调用序回放：每次 Stream 弹出一个脚本（事件切片）。
type scriptedProvider struct {
	mu      chanMu
	scripts [][]StreamEvent
	calls   []string // 每次请求的 Model
}

type chanMu struct{} // 占位（脚本弹出须串行——测试内单 goroutine 消费）

func (p *scriptedProvider) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	p.calls = append(p.calls, req.Model)
	out := make(chan StreamEvent, 8)
	script := p.scripts[0]
	p.scripts = p.scripts[1:]
	go func() {
		defer close(out)
		for _, ev := range script {
			out <- ev
		}
	}()
	return out
}

func textEndScript(text string) []StreamEvent {
	return []StreamEvent{
		{Kind: EvMessageStart},
		{Kind: EvTextDelta, TextDelta: text},
		{Kind: EvMessageEnd, StopReason: StopEndTurn},
	}
}

func retryableErrScript(msg string) []StreamEvent {
	return []StreamEvent{
		{Kind: EvMessageStart},
		{Kind: EvMessageEnd, StopReason: StopError, ErrorMessage: msg, Retryable: true},
	}
}

func fatalErrScript(msg string) []StreamEvent {
	return []StreamEvent{
		{Kind: EvMessageStart},
		{Kind: EvMessageEnd, StopReason: StopError, ErrorMessage: msg},
	}
}

func collectFO(ch <-chan StreamEvent) []StreamEvent {
	var out []StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func joined(events []StreamEvent) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString(ev.TextDelta)
		if ev.ErrorMessage != "" {
			b.WriteString("[" + ev.ErrorMessage + "]")
		}
	}
	return b.String()
}

func TestFailoverFallsBackOnRetryableError(t *testing.T) {
	p := &scriptedProvider{scripts: [][]StreamEvent{
		retryableErrScript("429 rate limited"),
		textEndScript("兜底模型的回答"),
	}}
	fo := NewFailover(p, []string{"primary", "fallback-1"})
	events := collectFO(fo.Stream(context.Background(), Request{Model: "primary"}))
	if got := joined(events); !strings.Contains(got, "兜底模型的回答") {
		t.Fatalf("events = %q", got)
	}
	if len(p.calls) != 2 || p.calls[0] != "primary" || p.calls[1] != "fallback-1" {
		t.Fatalf("calls = %v", p.calls)
	}
}

func TestFailoverPassesFatalErrorThrough(t *testing.T) {
	p := &scriptedProvider{scripts: [][]StreamEvent{
		fatalErrScript("invalid api key"),
	}}
	fo := NewFailover(p, []string{"primary", "fallback-1"})
	events := collectFO(fo.Stream(context.Background(), Request{Model: "primary"}))
	got := joined(events)
	if !strings.Contains(got, "invalid api key") {
		t.Fatalf("fatal error lost: %q", got)
	}
	if len(p.calls) != 1 {
		t.Fatalf("fatal error should not fall back, calls = %v", p.calls)
	}
}

func TestFailoverExhaustedKeepsRetryable(t *testing.T) {
	p := &scriptedProvider{scripts: [][]StreamEvent{
		retryableErrScript("429 a"),
		retryableErrScript("429 b"),
	}}
	fo := NewFailover(p, []string{"primary", "fallback-1"})
	events := collectFO(fo.Stream(context.Background(), Request{}))
	last := events[len(events)-1]
	if last.StopReason != StopError || !last.Retryable || last.ErrorMessage != "429 b" {
		t.Fatalf("last = %+v", last)
	}
}

func TestFailoverSingleModelPassthrough(t *testing.T) {
	p := &scriptedProvider{scripts: [][]StreamEvent{textEndScript("直接")}}
	if _, ok := NewFailover(p, []string{"only"}).(*failover); ok {
		t.Fatal("single model wrapped")
	}
}
