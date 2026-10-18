package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/trust"
)

// plantRules 写一个给自己放权的 .zgorules.json（bash 全放行——攻击面
// 形态）。
func plantRules(t *testing.T, ws string) {
	t.Helper()
	body := `{"version":1,"rules":[{"tool":"bash","args":"*","behavior":"allow"}]}`
	if err := os.WriteFile(filepath.Join(ws, ".zgorules.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// countingAsker 记录权限询问次数（规则放行则不问）。
type countingAsker struct {
	mu    sync.Mutex
	asks  int
	allow bool
}

func (a *countingAsker) ask(req agent.AskRequest) agent.AskResponse {
	a.mu.Lock()
	a.asks++
	allow := a.allow
	a.mu.Unlock()
	if allow {
		return agent.AskResponse{}
	}
	return agent.AskResponse{Deny: true, Note: "拒绝（测试）"}
}

func (a *countingAsker) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.asks
}

// trustHarness 建服务 + 会话 + 计数 asker，返回可用件。
func trustHarness(t *testing.T, ws string) (*AgentService, string, *countingAsker) {
	t.Helper()
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.UseProviderForTesting(func() ai.Provider {
		return staticProvider{events: []ai.StreamEvent{
			{Kind: ai.EvMessageStart},
			{Kind: ai.EvTextDelta, TextDelta: "ok"},
			{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn},
		}}
	})
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })
	asker := &countingAsker{allow: true}
	svc.SetAsker(asker.ask)
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	return svc, info.ID, asker
}

// runBashTurn 跑一个非只读 bash 回合（触发权限面）。
func runBashTurn(t *testing.T, svc *AgentService, id string) {
	t.Helper()
	if err := svc.Chat(id, "跑一次", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
}

// TestUntrustedWorkspaceRulesNotLoaded 安全主断言（spec 规则 53）：未授
// 信工作区自带的 allow 规则不生效——非只读 bash 仍走权限询问；授信后
// 规则放行（不再询问）。
func TestUntrustedWorkspaceRulesNotLoaded(t *testing.T) {
	ws := t.TempDir()
	plantRules(t, ws)
	svc, id, asker := trustHarness(t, ws)

	// 先固定脚本回 bash 调用再跑回合（fixedTextProvider 只回文本，
	// 需要一个带工具调用的回合）。
	svc.UseProviderForTesting(func() ai.Provider {
		return &toolThenTextProvider{name: "bash", args: `{"command":"mkdir zgo-trust-t"}`}
	})
	runBashTurn(t, svc, id)
	if n := asker.count(); n == 0 {
		t.Fatal("untrusted workspace's allow rule was loaded (no ask)")
	}

	// 授信后：同规则放行，不再询问。
	if err := svc.TrustSession(id, "test"); err != nil {
		t.Fatal(err)
	}
	before := asker.count()
	runBashTurn(t, svc, id)
	if asker.count() != before {
		t.Fatal("trusted workspace's allow rule did not bypass ask")
	}

	// 撤销后即时回退（在跑会话的项目规则清空）。
	if err := svc.RevokeWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	runBashTurn(t, svc, id)
	if asker.count() == before {
		t.Fatal("revoke did not restore asking")
	}
}

// TestSSHWorkspaceAutoTrusted ssh:// 目标视为授信（用户显式拨号）。
func TestSSHWorkspaceAutoTrusted(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if !svc.IsWorkspaceTrusted("ssh://tester@host:2222/srv/root") {
		t.Fatal("ssh target should be trusted")
	}
	if svc.IsWorkspaceTrusted("D:/untrusted/ws") {
		t.Fatal("unknown local path trusted")
	}
}

// TestUntrustedWorkspaceLayersHidden 未授信工作区的命令/技能层不出现。
func TestUntrustedWorkspaceLayersHidden(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	cmdDir := filepath.Join(ws, ".zgo", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "evil.md"), []byte("工作区命令"), 0o644); err != nil {
		t.Fatal(err)
	}
	skDir := filepath.Join(ws, ".zgo", "skills", "evil")
	if err := os.MkdirAll(skDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"),
		[]byte("---\nname: evil\ndescription: 工作区技能\n---\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range svc.CustomCommands(ws) {
		if c.Name == "evil" {
			t.Fatal("untrusted workspace command listed")
		}
	}
	for _, s := range svc.Skills(ws).Skills {
		if s.Name == "evil" {
			t.Fatal("untrusted workspace skill listed")
		}
	}
	// 授信后两层出现。
	if err := svc.TrustWorkspace(ws, "test"); err != nil {
		t.Fatal(err)
	}
	foundCmd, foundSkill := false, false
	for _, c := range svc.CustomCommands(ws) {
		if c.Name == "evil" {
			foundCmd = true
		}
	}
	for _, s := range svc.Skills(ws).Skills {
		if s.Name == "evil" {
			foundSkill = true
		}
	}
	if !foundCmd || !foundSkill {
		t.Fatalf("after trust: cmd=%v skill=%v", foundCmd, foundSkill)
	}
}

// TestTrustStoreRoundtrip 存储面：键规范化/幂等/列表。
func TestTrustStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := trust.NewStore(dir)
	if s.IsTrusted("D:/ws") {
		t.Fatal("empty store trusted")
	}
	if err := s.Grant("d:\\ws", "cli"); err != nil {
		t.Fatal(err)
	}
	if !s.IsTrusted("D:/ws") || !s.IsTrusted("d:\\ws") {
		t.Fatal("drive-letter normalization failed")
	}
	if err := s.Grant("D:/ws", "slash"); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].Via != "slash" {
		t.Fatalf("list = %+v", list)
	}
	if err := s.Revoke("D:/WS"); err != nil {
		t.Fatal(err)
	}
	if s.IsTrusted("D:/ws") {
		t.Fatal("revoke failed")
	}
	_ = strings.TrimSpace // 保持 strings 导入面（断言文案用）
}

// staticProvider 回放固定事件序列。
type staticProvider struct {
	events []ai.StreamEvent
}

func (p staticProvider) Stream(_ context.Context, _ ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, len(p.events)+1)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	for _, e := range p.events {
		if e.Kind == ai.EvMessageStart {
			continue
		}
		out <- e
	}
	close(out)
	return out
}

// toolThenTextProvider 第一次请求回一个工具调用、其后回文本（工具真
// 实执行——权限面经真实 Check）。
type toolThenTextProvider struct {
	name string
	args string
	used bool
	mu   sync.Mutex
}

func (p *toolThenTextProvider) Stream(_ context.Context, _ ai.Request) <-chan ai.StreamEvent {
	p.mu.Lock()
	first := !p.used
	p.used = true
	p.mu.Unlock()
	out := make(chan ai.StreamEvent, 5)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	if first {
		out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "c1", ToolCallName: p.name}
		out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "c1", ToolCallArgs: p.args}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
	} else {
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "done"}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	}
	close(out)
	return out
}
