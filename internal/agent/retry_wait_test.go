package agent

import (
	"testing"
	"time"
)

// TestRetryWaitRespectsServerHint 验证服务端指示的等待不被本地 30s cap
// 截短（issue #59：Retry-After:60s 被 cap 成 30s，重试全部提前砸在
// 限流窗口内，把「稍等片刻」恶化成必然失败）。
func TestRetryWaitRespectsServerHint(t *testing.T) {
	base := 500 * time.Millisecond
	if got := retryWait(60_000, base, 1); got != 60*time.Second {
		t.Fatalf("server hint 60s = %v", got)
	}
	if got := retryWait(120_000, base, 3); got != 120*time.Second {
		t.Fatalf("server hint 120s = %v", got)
	}
	// 服务端指示也有宽上限（5 分钟），防失控头。
	if got := retryWait(600_000, base, 1); got != 5*time.Minute {
		t.Fatalf("server hint over ceiling = %v", got)
	}
}

// TestRetryWaitExponentialCapsLocal 验证无服务端指示时本地指数退避仍受
// 30s cap（ rude 情况不受影响）。
func TestRetryWaitExponentialCapsLocal(t *testing.T) {
	base := 500 * time.Millisecond
	if got := retryWait(0, base, 1); got != time.Second {
		t.Fatalf("exp 1 = %v", got)
	}
	if got := retryWait(0, base, 10); got != retryBackoffCap {
		t.Fatalf("exp over cap = %v", got)
	}
	if got := retryWait(0, time.Millisecond, 1); got != 2*time.Millisecond {
		t.Fatalf("injected base ignored = %v", got)
	}
}
