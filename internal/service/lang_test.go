package service

// 语言解析与文案面测试（spec 规则 94）：settings.lang 优先、ZGO_LANG
// 兜底、预算通知与熔断错误按当前语言出文案。

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
	"github.com/skeleton1231/zcode-my-go/internal/i18n"

	"time"
)

func TestCopyRespectsLangSettings(t *testing.T) {
	t.Setenv("ZGO_LANG", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]string{"lang": "en"})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if c := svc.Copy(); c.Lang != i18n.En {
		t.Fatalf("settings.lang=en lost: %q", c.Lang)
	}
	// 环境兜底：无设置时 ZGO_LANG=en_US 生效。
	t.Setenv("ZGO_LANG", "en_US")
	svc2, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	if c := svc2.Copy(); c.Lang != i18n.En {
		t.Fatalf("env detection lost: %q", c.Lang)
	}
}

func TestBudgetNoticesLocalized(t *testing.T) {
	t.Setenv("ZGO_LANG", "")
	t.Setenv("LANG", "C")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"lang": "en", "budgetDailyTokens": 1000})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	if err := svc.SetHardBudgetStop(true); err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &langUsageProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	var notices []agent.Event
	if err := svc.Chat(info.ID, "one turn", func(ev agent.Event) {
		if ev.Kind == agent.EvBudgetNotice {
			notices = append(notices, ev)
		}
	}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notices {
		if strings.Contains(n.Text, "80%+") { // en Near 文案
			found = true
		}
		if strings.Contains(n.Text, "今日已用") {
			t.Fatalf("zh notice under en locale: %q", n.Text)
		}
	}
	if !found {
		t.Fatalf("no localized near notice: %+v", notices)
	}
	// 熔断错误同样本地化：再跑一回合把账推过线（闸在回合开始前判，
	// 第二回合累计跨线，第三次才被拒）。
	if err := svc.Chat(info.ID, "next", func(agent.Event) {}); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if err := svc.Chat(info.ID, "third", func(agent.Event) {}); err == nil ||
		!strings.Contains(err.Error(), "fused off") {
		t.Fatalf("fuse error not localized: %v", err)
	}
}

type langUsageProvider struct{}

func (p *langUsageProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "ok"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
		Usage: ai.Usage{InputTokens: 850, OutputTokens: 50}}
	close(out)
	return out
}

var _ = sync.Mutex{} // 保持导入面（并发场景复用此 provider 时加锁）

// TestServiceStringsLocalized（spec 规则 113）：会话层错误与通知按语
// 言出——en 服务下空消息/重复回合是英文；zh 基准不变。

// blockStreamProvider 挂住流直到关闭（撑运行态）。
type blockStreamProvider struct {
	done chan struct{}
	once sync.Once
}

func (p *blockStreamProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 2)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	go func() {
		<-p.done
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		close(out)
	}()
	return out
}

func TestSessionErrorsLocalized(t *testing.T) {
	t.Setenv("ZGO_LANG", "")
	t.Setenv("LANG", "C")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]string{"lang": "en"})
	os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600)
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "  ", func(agent.Event) {}); err == nil ||
		!strings.Contains(err.Error(), "empty message") {
		t.Fatalf("empty err = %v（en）", err)
	}
	// 撑运行态再发第二条：先等 Running=true（直接抢发第二条会跟
	// hold 竞争运行态——测试自己死锁过）。
	p := &blockStreamProvider{done: make(chan struct{})}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	go func() {
		svc.Chat(info.ID, "hold", func(agent.Event) {})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running := false
		for _, si := range svc.ListSessions() {
			if si.ID == info.ID {
				running = si.Running
			}
		}
		if running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	err = svc.Chat(info.ID, "second", func(agent.Event) {})
	p.once.Do(func() { close(p.done) })
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("in-progress err = %v（en）", err)
	}
}
