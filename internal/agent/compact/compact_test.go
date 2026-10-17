package compact

import (
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestThreshold verifies the budget math: window minus output reserve minus
// buffer, scaled by the threshold percent (ZCode's effective window).
func TestThreshold(t *testing.T) {
	// 200k - 21k - 13k = 166k
	if got := DefaultPolicy().Threshold(); got != 166_000 {
		t.Fatalf("default threshold = %d", got)
	}
	p := DefaultPolicy()
	p.ThresholdPercent = 80
	if got := p.Threshold(); got != 132_800 {
		t.Fatalf("80%% threshold = %d", got)
	}
	// 预留超过窗口时收敛到 21k 上限，窗口不足则为 0。
	p = DefaultPolicy()
	p.ContextWindow = 10_000
	if got := p.Threshold(); got != 0 {
		t.Fatalf("tiny window threshold = %d", got)
	}
}

// TestDecide verifies every reason branch.
func TestDecide(t *testing.T) {
	p := DefaultPolicy()
	if d := Decide(p, 100_000, 10, 0); d.Reason != ReasonBelowThreshold || d.ShouldCompact {
		t.Fatalf("below: %+v", d)
	}
	if d := Decide(p, 170_000, 10, 0); !d.ShouldCompact || d.Reason != ReasonAboveThreshold {
		t.Fatalf("above: %+v", d)
	}
	if d := Decide(p, 999_999, 1, 0); d.Reason != ReasonNotEnough {
		t.Fatalf("not enough: %+v", d)
	}
	if d := Decide(p, 999_999, 10, 3); d.Reason != ReasonCircuitBreaker || d.ShouldCompact {
		t.Fatalf("breaker: %+v", d)
	}
	p.Enabled = false
	if d := Decide(p, 999_999, 10, 0); d.Reason != ReasonDisabled {
		t.Fatalf("disabled: %+v", d)
	}
}

// TestEstimateTokens verifies the ratio and overhead.
func TestEstimateTokens(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "1234567890"}}}, // 10 runes → 4 + 16
	}
	if got := EstimateTokens(msgs); got != 20 {
		t.Fatalf("estimate = %d", got)
	}
}

func TestSummaryMessage(t *testing.T) {
	m := SummaryMessage("做完了")
	if m.Role != ai.RoleUser || m.Blocks[0].Text != "（会话已压缩；以下是此前对话的摘要）\n\n做完了" {
		t.Fatalf("message = %+v", m)
	}
}
