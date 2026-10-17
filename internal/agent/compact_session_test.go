package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent/compact"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// capturingProvider 记录每次请求的消息，返回一段固定摘要。
type capturingProvider struct {
	requests [][]ai.Message
}

func (p *capturingProvider) Stream(_ context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.requests = append(p.requests, req.Messages)
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "摘要内容"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// TestCompactSummarizesOnlyDroppedPrefix 验证摘要输入只含被丢弃的前缀，
// 不含保留尾部（issue #57：尾部原样留在上下文，送摘要纯浪费 token 且
// 摘要重复转述）。
func TestCompactSummarizesOnlyDroppedPrefix(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	cap := &capturingProvider{}
	m.Wire(Wiring{
		Provider: func() ai.Provider { return cap },
		Model:    func() string { return "fake-model" },
	})
	s, _ := m.New("ws")
	var msgs []ai.Message
	for i := 0; i < 10; i++ {
		role := ai.RoleUser
		if i%2 == 1 {
			role = ai.RoleAssistant
		}
		msg := ai.Message{Role: role, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "m" + string(rune('0'+i))}}}
		msgs = append(msgs, msg)
		if err := s.Append(msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(cap.requests) != 1 {
		t.Fatalf("provider calls = %d", len(cap.requests))
	}
	req := cap.requests[0]
	// 请求 = toSummarize(前 4 条) + 摘要 prompt（最后一条）。
	if len(req) != 5 {
		t.Fatalf("request messages = %d, want 4+prompt", len(req))
	}
	for _, m := range req[:4] {
		if m.Text() == "m9" || m.Text() == "m8" || m.Text() == "m7" {
			t.Fatalf("kept tail sent to summary: %q", m.Text())
		}
	}
	// 结果 = 摘要 + 保留尾部。
	after := s.Messages()
	if len(after) != compact.DefaultKeepTail+1 || after[0].Text() == "" {
		t.Fatalf("after compact = %d messages", len(after))
	}
	if !strings.Contains(after[0].Text(), "摘要内容") {
		t.Fatalf("summary missing: %q", after[0].Text())
	}
}

// TestBoundedSummaryInput 验证裁剪：超预算时从最旧端丢起，摘要请求
// 永远装得下（issue #57：近阈值时请求自身超限 → 400 → 三次熔断，
// 压缩在最需要时死锁式停用）。
func TestBoundedSummaryInput(t *testing.T) {
	big := ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{
		{Kind: ai.BlockText, Text: strings.Repeat("字", 10_000)}}} // ~10k token
	small := ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{
		{Kind: ai.BlockText, Text: "短"}}}

	// 预算只够两条短消息：最旧的大消息被丢掉。
	got := boundedSummaryInput([]ai.Message{big, small, small}, 8192+4096+100)
	if len(got) != 2 || got[0].Text() != "短" {
		t.Fatalf("bounded = %+v", got)
	}
	// 全部超预算：返回空（Compact 报 nothing to compact）。
	if got := boundedSummaryInput([]ai.Message{big}, 8192+4096+100); len(got) != 0 {
		t.Fatalf("oversized kept: %+v", got)
	}
	// 宽预算：原样。
	if got := boundedSummaryInput([]ai.Message{small, small}, 200_000); len(got) != 2 {
		t.Fatalf("loose budget trimmed: %+v", got)
	}
}
