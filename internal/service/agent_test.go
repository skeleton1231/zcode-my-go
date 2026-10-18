package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// sseAnthropic 返回一段完整文本回合的 Anthropic SSE 流。
func sseAnthropic() *httptest.Server {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
}

// scrubEnv 清空影响 resolve() 的环境变量：本机可能全局设置
// ANTHROPIC_BASE_URL 等（如 GLM 端点），不清空会顶掉测试的设置。
func scrubEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ZGO_MODEL", "ZGO_API"} {
		t.Setenv(k, "")
	}
}

// newService 在临时目录构建服务（首次运行，无设置文件）。
func newService(t *testing.T) *AgentService {
	t.Helper()
	scrubEnv(t)
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestNewFirstRun 验证无设置文件时首次运行成功且视图为默认值。
func TestNewFirstRun(t *testing.T) {
	s := newService(t)
	v := s.GetSettings()
	if v.BaseURL != DefaultBaseURL {
		t.Fatalf("baseUrl = %q, want default", v.BaseURL)
	}
	if v.API != "anthropic" || v.HasAPIKey {
		t.Fatalf("view = %+v", v)
	}
}

// TestSaveSettingsRoundtrip 验证保存、KeyHint 脱敏、空 key 保留旧值、
// 重启后仍能读回。
func TestSaveSettingsRoundtrip(t *testing.T) {
	scrubEnv(t)
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := sseAnthropic()
	t.Cleanup(srv.Close)
	v, err := s.SaveSettings("anthropic", srv.URL, "sk-ant-1234567890abcd", "fake-model", "off")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !v.HasAPIKey || v.KeyHint != "sk-…abcd" {
		t.Fatalf("key hint = %q", v.KeyHint)
	}
	if v.BaseURL != srv.URL || v.Model != "fake-model" {
		t.Fatalf("view = %+v", v)
	}
	// 空 key：保留已存 key。
	v, err = s.SaveSettings("anthropic", srv.URL, "", "other-model", "low")
	if err != nil {
		t.Fatal(err)
	}
	if !v.HasAPIKey {
		t.Fatal("empty apiKey dropped the stored key")
	}
	// 非法 api 值被忽略。
	if _, err := s.SaveSettings("bogus", srv.URL, "", "m", "off"); err != nil {
		t.Fatal(err)
	}
	if s.GetSettings().API != "anthropic" {
		t.Fatalf("invalid api accepted: %+v", s.GetSettings())
	}
	// 重启读回。
	again, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(again.Close)
	if v := again.GetSettings(); !v.HasAPIKey || v.BaseURL != srv.URL {
		t.Fatalf("settings not persisted: %+v", v)
	}
}

// TestResolveEnvOverrides 验证环境变量覆盖生效。
func TestResolveEnvOverrides(t *testing.T) {
	s := newService(t)
	t.Setenv("ANTHROPIC_BASE_URL", "http://env.example")
	t.Setenv("ZGO_MODEL", "env-model")
	t.Setenv("ZGO_API", "openai")
	v := s.GetSettings()
	if v.BaseURL != "http://env.example" || v.Model != "env-model" || v.API != "openai" {
		t.Fatalf("env overrides not applied: %+v", v)
	}
}

// TestSessionLifecycle 验证会话的建/列/取/删。
func TestSessionLifecycle(t *testing.T) {
	s := newService(t)
	ws := t.TempDir()
	info, err := s.NewSession(ws)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	found := false
	for _, it := range s.ListSessions() {
		if it.ID == info.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("new session missing from list")
	}
	msgs, err := s.Messages(info.ID)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("messages = %v, err = %v", msgs, err)
	}
	if err := s.DeleteSession(info.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Messages(info.ID); err == nil {
		t.Fatal("messages on deleted session should fail")
	}
	// 空 workspace 回退到当前目录。
	def, err := s.NewSession("")
	if err != nil {
		t.Fatalf("NewSession default: %v", err)
	}
	cwd, _ := os.Getwd()
	if def.Workspace != cwd {
		t.Fatalf("default workspace = %q, want cwd %q", def.Workspace, cwd)
	}
}

// TestChatStreamsAndPersists 验证 UI 唯一入口的完整链路：设置→会话→
// Chat 流式→落盘。SSE 由 httptest 提供。
func TestChatStreamsAndPersists(t *testing.T) {
	scrubEnv(t)
	s := newService(t)
	srv := sseAnthropic()
	t.Cleanup(srv.Close)
	if _, err := s.SaveSettings("anthropic", srv.URL, "sk-test", "fake-model", "off"); err != nil {
		t.Fatal(err)
	}
	info, err := s.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var events []agent.Event
	err = s.Chat(info.ID, "hi", func(ev agent.Event) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	sawEnd := false
	for _, ev := range events {
		if ev.Kind == agent.EvAgentEnd && ev.StopReason == ai.StopEndTurn {
			sawEnd = true
		}
	}
	if !sawEnd {
		t.Fatalf("no end_turn agent_end in %d events", len(events))
	}
	msgs, err := s.Messages(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != ai.RoleUser || msgs[1].Text() != "hello" {
		t.Fatalf("transcript = %+v", msgs)
	}
	// 重启后可重放。
	again, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(again.Close)
	_ = again // 会话在各自的 dataDir；重放已在 agent 包覆盖
}

// TestUnknownSessionErrors 验证未知会话在各入口统一报错。
func TestUnknownSessionErrors(t *testing.T) {
	s := newService(t)
	if err := s.Chat("nope", "x", func(agent.Event) {}); err == nil {
		t.Fatal("Chat on unknown session should fail")
	}
	if err := s.Retry("nope", func(agent.Event) {}); err == nil {
		t.Fatal("Retry on unknown session should fail")
	}
	if err := s.EditAndResend("nope", "x", func(agent.Event) {}); err == nil {
		t.Fatal("EditAndResend on unknown session should fail")
	}
	if err := s.CompactSession("nope"); err == nil {
		t.Fatal("CompactSession on unknown session should fail")
	}
	if err := s.PlanMode("nope", true); err == nil {
		t.Fatal("PlanMode on unknown session should fail")
	}
	// Abort 对未知会话是静默 no-op（与 UI 语义一致），不应 panic。
	s.Abort("nope")
}

// TestPlanModeAndModelSuggestions 验证模式切换与目录建议。
func TestPlanModeAndModelSuggestions(t *testing.T) {
	s := newService(t)
	info, err := s.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PlanMode(info.ID, true); err != nil {
		t.Fatalf("plan mode on: %v", err)
	}
	if err := s.PlanMode(info.ID, false); err != nil {
		t.Fatalf("plan mode off: %v", err)
	}
	if sug := s.ModelSuggestions(); len(sug) == 0 {
		t.Fatal("no model suggestions")
	}
}

// TestMemorySectionInSystem 验证 workspace 记忆按需创建且幂等。
func TestMemoryIsPerWorkspace(t *testing.T) {
	s := newService(t)
	a, b := t.TempDir(), t.TempDir()
	if s.memoryFor(a) == nil || s.memoryFor(b) == nil {
		t.Fatal("nil memory")
	}
	if s.memoryFor(a) == s.memoryFor(b) {
		t.Fatal("workspaces share a memory store")
	}
	if s.memoryFor(a) != s.memoryFor(a) {
		t.Fatal("memory not cached per workspace")
	}
}

// TestContinueSessionPicksLatestInWorkspace 验证 --continue 的候选选择：
// 同工作区取最近修改的会话，其他工作区忽略，无候选明确返回 false。
// 间隔 2ms 创建——时钟粒度不足以区分时会话 UpdatedAt 不可比。
func TestContinueSessionPicksLatestInWorkspace(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	wsA, wsB := t.TempDir(), t.TempDir()

	older, err := svc.NewSession(wsA)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	other, err := svc.NewSession(wsB)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	newer, err := svc.NewSession(wsA)
	if err != nil {
		t.Fatal(err)
	}
	if older.ID == newer.ID || older.ID == other.ID {
		t.Fatalf("session ids collided: %q %q %q", older.ID, other.ID, newer.ID)
	}

	got, ok := svc.ContinueSession(wsA)
	if !ok {
		t.Fatal("no candidate for wsA")
	}
	if got.ID != newer.ID {
		t.Fatalf("candidate = %q, want newest %q", got.ID, newer.ID)
	}
	if _, ok := svc.ContinueSession(t.TempDir()); ok {
		t.Fatal("empty workspace must report no candidate")
	}
}

// TestSessionIDNoCollisionOnSameTick 验证同一时钟节拍内的并发/连续创建
// 不撞 ID（Windows 时钟粒度百纳秒级时 Format 时间戳会同值——曾让两个
// 会话互相覆盖）。
func TestSessionIDNoCollisionOnSameTick(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ws := t.TempDir()
	ids := make(map[string]bool)
	for i := 0; i < 50; i++ {
		info, err := svc.NewSession(ws)
		if err != nil {
			t.Fatal(err)
		}
		if ids[info.ID] {
			t.Fatalf("duplicate session id %q at iteration %d", info.ID, i)
		}
		ids[info.ID] = true
	}
}

// TestUsageSummaryAndFormat 验证用量面（spec 规则 25）：脚本回合的
// usage 记账汇入 UsageSummaryOf（含缓存读与回合数），FormatUsage 的表
// 含命中率；零用量明确展示零值。
func TestUsageSummaryAndFormat(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "done"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
		Usage: ai.Usage{InputTokens: 100, OutputTokens: 40,
			CacheReadTokens: 60, CacheWriteTokens: 10}}
	close(out)
	svc.UseProviderForTesting(func() ai.Provider {
		return chProvider{ch: out}
	})
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })

	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "做点事", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}

	sum, err := svc.UsageSummaryOf(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Usage.InputTokens != 100 || sum.Usage.CacheReadTokens != 60 {
		t.Fatalf("usage = %+v", sum.Usage)
	}
	if sum.Turns != 1 {
		t.Fatalf("turns = %d", sum.Turns)
	}
	formatted := FormatUsage(sum)
	for _, want := range []string{"输入      100", "输出      40", "缓存读    60", "缓存写    10", "回合 1", "37%"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("missing %q in:\n%s", want, formatted)
		}
	}

	// 零用量：无命中率行、字段全零。
	zero, err := svc.UsageSummaryOf(mustNewSession(t, svc))
	if err != nil {
		t.Fatal(err)
	}
	zf := FormatUsage(zero)
	if strings.Contains(zf, "命中") || !strings.Contains(zf, "输入      0") {
		t.Fatalf("zero format wrong:\n%s", zf)
	}
}

// chProvider 把预置事件通道当流（一次性）。
type chProvider struct{ ch <-chan ai.StreamEvent }

func (p chProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	return p.ch
}

func mustNewSession(t *testing.T, svc *AgentService) string {
	t.Helper()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return info.ID
}

// TestDailyBudgetLedgerAndNotice 验证每日预算守卫（spec 规则 35）：回合
// 增量入账（跨会话共享同一记账）、今日行进入用量面、阈值各通知一次。
func TestDailyBudgetLedgerAndNotice(t *testing.T) {
	dir := t.TempDir()
	// 预算随服务启动加载——先写 settings 再 New。
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"budgetDailyTokens":200}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()

	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var events []agent.Event
	for _, u := range []ai.Usage{
		{InputTokens: 100, OutputTokens: 10}, // →110
		{InputTokens: 60, OutputTokens: 10},  // →+70 = 180（≥80%×200）
	} {
		ch := make(chan ai.StreamEvent, 3)
		ch <- ai.StreamEvent{Kind: ai.EvMessageStart}
		ch <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "ok"}
		ch <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn, Usage: u}
		close(ch)
		prov := ch
		svc.UseProviderForTesting(func() ai.Provider { return fixedProvider{ch: prov} })
		if err := svc.Chat(info.ID, "做", func(ev agent.Event) { events = append(events, ev) }); err != nil {
			t.Fatal(err)
		}
	}

	used, budget := svc.DailyUsage()
	if used != 180 || budget != 200 { // 100+10 + 60+10 = 180
		t.Fatalf("daily = %d/%d, want 180/200", used, budget)
	}
	notices := 0
	for _, ev := range events {
		if ev.Kind == agent.EvBudgetNotice {
			notices++
			if !strings.Contains(ev.Text, "80%") {
				t.Errorf("notice text = %q", ev.Text)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("notices = %d, want 1", notices)
	}

	// 新服务实例重放同一记账（跨进程持久）。
	svc2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	if used2, _ := svc2.DailyUsage(); used2 != 180 {
		t.Fatalf("replayed daily = %d", used2)
	}
}

// fixedProvider 回一段带 usage 的完整回合。
type fixedProvider struct {
	usage ai.Usage
	ch    <-chan ai.StreamEvent
}

func (p fixedProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	return p.ch
}

// TestCustomCommandTextCapped 验证命令文件封顶（issue #195 家族清单
// 的第五个入口——60K rune 对齐 stdin/粘贴/资源口径；超限截断 + 标注）。
func TestCustomCommandTextCapped(t *testing.T) {
	svc, _ := New(t.TempDir())
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	dir := filepath.Join(ws, ".zgo", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("字", commandCapRunes+9)
	if err := os.WriteFile(filepath.Join(dir, "big.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds := svc.CustomCommands(ws)
	if len(cmds) != 1 || cmds[0].Name != "big" {
		t.Fatalf("cmds = %+v", cmds)
	}
	if n := len([]rune(cmds[0].Text)); n > commandCapRunes+40 {
		t.Fatalf("command text uncapped: %d runes", n)
	}
	if !strings.Contains(cmds[0].Text, "已截断") {
		t.Fatal("cap annotation missing")
	}
}
