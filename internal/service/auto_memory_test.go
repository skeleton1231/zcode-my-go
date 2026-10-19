package service

// 收尾自动记忆（spec 规则 74）的行为测试：脚本 provider 的请求序列
// 是 回合 → 自动命名 → Close 时的记忆补全；落盘断言走 memory 文件本
// 体；去重（当日/每会话一次）与开关各有专测。

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// memScriptProvider：1=回合，2=命名，3+=记忆补全（回 facts）。
type memScriptProvider struct {
	mu    sync.Mutex
	reqs  int
	facts string
}

func (p *memScriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.mu.Lock()
	p.reqs++
	n := p.reqs
	p.mu.Unlock()
	text := "回合回复"
	switch {
	case n == 2:
		text = "会话标题"
	case n >= 3:
		text = p.facts
	}
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: text}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func (p *memScriptProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqs
}

func TestAutoMemoryAppendsOnClose(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &memScriptProvider{facts: "用户偏好：中文交流\n项目约定：先补测试再实现"}
	svc.UseProviderForTesting(func() ai.Provider { return p })

	ws := t.TempDir()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "修一下登录样式，记得先补测试", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// 等自动命名落地（回合后后台 goroutine，Close 前序列确定性：1 回
	// 合 + 2 命名）。
	waitForCond(t, func() bool { return p.requestCount() >= 2 })

	svc.Close()

	mem := svc.memoryFor(ws)
	content := mem.Read()
	if !strings.Contains(content, "用户偏好：中文交流") || !strings.Contains(content, "项目约定") {
		t.Fatalf("memory missing facts:\n%s", content)
	}
	// 带时间戳条目形态（Append 的 ## 头）。
	if !strings.Contains(content, "## "+time.Now().Format("2006-01-02")) {
		t.Fatalf("entry not timestamped:\n%s", content)
	}
	// 落盘文件（memoryFor 的 Read 有 4KB 尾部窗口——文件本体直查）。
	raw := readMemoryFile(t, svc, ws)
	if !strings.Contains(raw, "用户偏好：中文交流") {
		t.Fatalf("facts not persisted:\n%s", raw)
	}
}

func TestAutoMemoryDedupPerDay(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &memScriptProvider{facts: "第一条记忆"}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	ws := t.TempDir()
	a, _ := svc.NewSession(ws)
	if err := svc.Chat(a.ID, "问题一", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	waitForCond(t, func() bool { return p.requestCount() >= 2 })
	svc.Close()
	// Close 后 mgr 里的会话仍在（Close 不删会话）——再次 Close 不重
	// 发（每会话每进程一次）；即便新进程语义由当日去重兜底。
	base := p.requestCount()
	if base < 3 {
		t.Fatalf("memory completion never fired: %d", base)
	}
	svc.Close()
	if got := p.requestCount(); got != base {
		t.Fatalf("re-fired on second Close: %d -> %d", base, got)
	}
}

func TestAutoMemoryOff(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.DisableAutoMemoryForTesting()
	p := &memScriptProvider{facts: "不该出现的记忆"}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	ws := t.TempDir()
	info, _ := svc.NewSession(ws)
	if err := svc.Chat(info.ID, "一次性问题", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	waitForCond(t, func() bool { return p.requestCount() >= 2 })
	svc.Close()
	if content := svc.memoryFor(ws).Read(); content != "" {
		t.Fatalf("memory written while disabled:\n%s", content)
	}
}

func TestSanitizeMemoryFacts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"none", ""},
		{"无", ""},
		{"- 用户偏好：中文\n- 项目约定：测试先行", "用户偏好：中文\n项目约定：测试先行"},
		{"```\n用户偏好：中文\n```", "用户偏好：中文"},
		{"用户偏好：中文\nnone\n第二条\n第三条\n第四条", "用户偏好：中文\n第二条\n第三条"},
		{"  \n\n", ""},
	}
	for _, c := range cases {
		if got := sanitizeMemoryFacts(c.in); got != c.want {
			t.Fatalf("sanitize(%q) = %q (want %q)", c.in, got, c.want)
		}
	}
	// 长行截断。
	long := strings.Repeat("长", 200)
	got := sanitizeMemoryFacts(long)
	if len([]rune(got)) > 121 { // 120 + 省略号
		t.Fatalf("long line not truncated: %d runes", len([]rune(got)))
	}
}

// waitForCond 是 waitForTitle 的通用版（≤5s）。
func waitForCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readMemoryFile 直接读记忆落盘文件（绕过 Read 的尾部窗口）。
func readMemoryFile(t *testing.T, svc *AgentService, ws string) string {
	t.Helper()
	raw, err := os.ReadFile(toolsMemoryPath(svc, ws))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// toolsMemoryPath 用与 memoryFor 同源的路径规则（tools.MemoryPath）。
func toolsMemoryPath(svc *AgentService, ws string) string {
	sum := sha256.Sum256([]byte(ws))
	return filepath.Join(svc.memRoot, fmt.Sprintf("ws_%x.md", sum[:16]))
}
