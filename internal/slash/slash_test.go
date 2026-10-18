package slash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/export"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func newEnv(t *testing.T) (*Env, *snapshotLines) {
	t.Helper()
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	var mu sync.Mutex
	return &Env{
		Svc:        svc,
		SessionID:  func() string { return info.ID },
		Workspace:  func() string { return info.Workspace },
		SetSession: func(agent.SessionInfo) {},
		// Out 会被 login 等后台 goroutine 调用（-race 在 CI 上逮过与
		// 测试主 goroutine 读 *out 的竞态）——追加持锁，读经 snapshot。
		Out: func(msg string) {
			mu.Lock()
			lines = append(lines, msg)
			mu.Unlock()
		},
	}, &snapshotLines{mu: &mu, lines: &lines}
}

// snapshotLines 暴露持锁快照（len/索引/包含都走它——裸读 *lines 会与
// 后台追加竞态）。
type snapshotLines struct {
	mu    *sync.Mutex
	lines *[]string
}

func (s *snapshotLines) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), *s.lines...)
}

func (s *snapshotLines) Len() int      { return len(s.snapshot()) }
func (s *snapshotLines) All() []string { return s.snapshot() }
func (s *snapshotLines) Contains(want string) bool {
	return strings.Contains(strings.Join(s.snapshot(), "\n"), want)
}

func TestRunDispatch(t *testing.T) {
	env, out := newEnv(t)
	if handled, quit := Run(env, "普通问题"); handled || quit {
		t.Fatal("chat line swallowed")
	}
	for _, q := range []string{"/exit", "/quit", "/q", "/EXIT"} {
		if _, quit := Run(env, q); !quit {
			t.Fatalf("%s did not quit", q)
		}
	}
	Run(env, "/nope")
	if last := out.All()[out.Len()-1]; !strings.Contains(last, "/help") {
		t.Fatalf("unknown hint = %q", last)
	}
	Run(env, "/help")
	joined := strings.Join(out.All(), "\n")
	for _, name := range []string{"/new", "/compact", "/plan", "/model", "/rename", "/status", "/exit"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("/help missing %s", name)
		}
	}
}

func TestRunModelRenameStatusPlan(t *testing.T) {
	env, out := newEnv(t)
	Run(env, "/model glm-5.2")
	if v := env.Svc.GetSettings(); v.Model != "glm-5.2" {
		t.Fatalf("model = %q", v.Model)
	}
	Run(env, "/rename 桌面调试")
	found := false
	for _, s := range env.Svc.ListSessions() {
		if s.Title == "桌面调试" {
			found = true
		}
	}
	if !found {
		t.Fatal("rename not applied")
	}
	Run(env, "/status")
	if last := out.All()[out.Len()-1]; !strings.Contains(last, "glm-5.2") || !strings.Contains(last, "上下文") {
		t.Fatalf("status = %q", last)
	}
	Run(env, "/plan on")
	if !env.Svc.PlanModeState(env.SessionID()) {
		t.Fatal("plan on not applied")
	}
	Run(env, "/plan off")
	if env.Svc.PlanModeState(env.SessionID()) {
		t.Fatal("plan off not applied")
	}
}

func TestMatchForHints(t *testing.T) {
	all := Match("/")
	if len(all) < 6 {
		t.Fatalf("all commands = %d", len(all))
	}
	// 前缀过滤。
	m := Match("/mo")
	if len(m) != 1 || m[0].Usage != "/model <模型ID>" {
		t.Fatalf("/mo = %+v", m)
	}
	// 完整命令名（含参数提示）也算命中。
	if m := Match("/plan on"); len(m) != 1 || m[0].Usage != "/plan [on|off]" {
		t.Fatalf("/plan on = %+v", m)
	}
	// 非命令前缀返回空。
	if m := Match("普通"); m != nil {
		t.Fatalf("plain = %+v", m)
	}
	// 上限截断。
	if got := len(Match("/")); got > 8 {
		t.Fatalf("cap exceeded: %d", got)
	}
}

// TestLoginCommandNotLoggedIn verifies /login reports the flow start (the
// browser round-trip is env-dependent; the status path is deterministic).
func TestLoginCommand(t *testing.T) {
	env, out := newEnv(t)
	// 已登录状态：直接报告到期时间。
	dir := t.TempDir()
	_ = dir
	// 未登录：启动提示（LoginStart 真正起回调服务器——这里只验证命令可
	// 派发且不 panic；链接通知异步到达）。
	Run(env, "/login")
	time.Sleep(50 * time.Millisecond)
	joined := strings.Join(out.All(), "\n")
	if !strings.Contains(joined, "OAuth") && !strings.Contains(joined, "授权") {
		t.Fatalf("login output = %q", joined)
	}
}

// TestSlashResume 验证 /resume 三形态（spec desktop-shell 规则 23）：无
// 参切当前工作区最近会话、前缀指定（可跨工作区）、未知名可读报错；切
// 到当前会话不重复切换。
func TestSlashResume(t *testing.T) {
	env, _ := newEnv(t)
	ws := env.Workspace() // 无参形态按当前工作区取最近会话
	older, err := env.Svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	newer, err := env.Svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}

	// 无参：切到工作区最近会话（newer），SetSession 收到它。
	got := ""
	env.SetSession = func(info agent.SessionInfo) { got = info.ID }
	if handled, _ := Run(env, "/resume"); !handled {
		t.Fatal("/resume not handled")
	}
	if got != newer.ID {
		t.Fatalf("resumed = %q, want %q", got, newer.ID)
	}

	// 前缀：切回 older（同工作区前缀唯一）。
	env.SessionID = func() string { return newer.ID }
	if handled, _ := Run(env, "/resume "+older.ID); !handled {
		t.Fatal("prefix form not handled")
	}
	if got != older.ID {
		t.Fatalf("prefix resumed = %q, want %q", got, older.ID)
	}

	// 已在该会话：不切换（SetSession 不被调用）。
	called := false
	env.SetSession = func(agent.SessionInfo) { called = true }
	env.SessionID = func() string { return older.ID }
	out := captureOut(t, env, "/resume "+older.ID)
	if called || !strings.Contains(out, "已在") {
		t.Fatalf("same-session: called=%v out=%q", called, out)
	}

	// 未知名：可读报错，不切换。
	out = captureOut(t, env, "/resume zzz")
	if called || !strings.Contains(out, "没有会话") {
		t.Fatalf("unknown: called=%v out=%q", called, out)
	}
}

// captureOut 跑一条命令并捕获 env.Out 的输出。
func captureOut(t *testing.T, env *Env, line string) string {
	t.Helper()
	var lines []string
	prev := env.Out
	env.Out = func(msg string) { lines = append(lines, msg) }
	defer func() { env.Out = prev }()
	Run(env, line) // run 的返回串经 env.Out 送达
	return strings.Join(lines, "\n")
}

// TestSlashUsage 验证 /usage（spec 规则 25）：零用量会话展示零值表。
func TestSlashUsage(t *testing.T) {
	env, lines := newEnv(t)
	if handled, _ := Run(env, "/usage"); !handled {
		t.Fatal("/usage not handled")
	}
	joined := strings.Join(lines.All(), "\n")
	for _, want := range []string{"[用量]", "输入      0", "输出      0", "回合 0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "命中") {
		t.Errorf("zero usage must not show a hit rate:\n%s", joined)
	}
}

// TestSlashMCP 验证 /mcp（spec 规则 28）：无服务器时空列表说明、未知名
// 报错并列表、缺 . 分隔报用法；Send 钩子未装的表面拿到展开文本。
func TestSlashMCP(t *testing.T) {
	env, lines := newEnv(t)

	out := captureOutSlash(t, env, "/mcp")
	if !strings.Contains(out, "没有可用的提示词") {
		t.Fatalf("empty list: %q", out)
	}
	out = captureOutSlash(t, env, "/mcp helper.review")
	if !strings.Contains(out, "mcp 失败") {
		t.Fatalf("unknown prompt: %q", out)
	}
	out = captureOutSlash(t, env, "/mcp no-dot")
	if !strings.Contains(out, "用法") {
		t.Fatalf("missing dot: %q", out)
	}
	_ = lines
}

// captureOutSlash 跑一条命令并收集 Out（复用 /resume 的形态，独立命名
// 避免与既有助手冲突）。
func captureOutSlash(t *testing.T, env *Env, line string) string {
	t.Helper()
	var lines []string
	prev := env.Out
	env.Out = func(msg string) { lines = append(lines, msg) }
	defer func() { env.Out = prev }()
	Run(env, line)
	return strings.Join(lines, "\n")
}

// TestSlashRetryResend 验证终端回合重做（spec 规则 29）：/retry 无历史
// 明确拒绝、有历史经 Rerun 钩子收到原文；/resend 缺文本报用法、带文本
// 传新文；钩子缺失说明不支持。
func TestSlashRetryResend(t *testing.T) {
	env, _ := newEnv(t)

	// 无历史回合。
	out := captureOutSlash(t, env, "/retry")
	if !strings.Contains(out, "没有可重跑") {
		t.Fatalf("empty history: %q", out)
	}

	// 攒一个历史回合（直接经 service 写转录：chat 走 provider 成本高）。
	if err := seedTurn(t, env); err != nil {
		t.Fatal(err)
	}
	var got string
	env.Rerun = func(text string) error { got = text; return nil }
	out = captureOutSlash(t, env, "/retry")
	if !strings.Contains(out, "[重跑]") || got != "第一回合的问题" {
		t.Fatalf("retry: out=%q got=%q", out, got)
	}

	out = captureOutSlash(t, env, "/resend 改写后的问题")
	if !strings.Contains(out, "[重跑]") || got != "改写后的问题" {
		t.Fatalf("resend: out=%q got=%q", out, got)
	}

	out = captureOutSlash(t, env, "/resend")
	if !strings.Contains(out, "用法") {
		t.Fatalf("bare resend: %q", out)
	}

	env.Rerun = nil
	out = captureOutSlash(t, env, "/retry")
	if !strings.Contains(out, "不支持") {
		t.Fatalf("no hook: %q", out)
	}
}

// seedTurn 经一次真实 Chat 攒一个用户回合（脚本回放太重：用最小
// provider）。
func seedTurn(t *testing.T, env *Env) error {
	t.Helper()
	env.Svc.UseProviderForTesting(func() ai.Provider {
		out := make(chan ai.StreamEvent, 3)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "回答"}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		close(out)
		return chProviderSvc{ch: out}
	})
	defer env.Svc.UseProviderForTesting(nil)
	return env.Svc.Chat(env.SessionID(), "第一回合的问题", func(agent.Event) {})
}

type chProviderSvc struct{ ch <-chan ai.StreamEvent }

func (p chProviderSvc) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	return p.ch
}

// TestSlashMCPResource 验证 /mcp 的资源分支（spec 规则 31）：缺冒号报
// 用法、未知服务器报错并列表、Send 钩子收到围栏内容；列表含资源段。
func TestSlashMCPResource(t *testing.T) {
	env, _ := newEnv(t)

	out := captureOutSlash(t, env, "/mcp @nosep")
	if !strings.Contains(out, "用法") {
		t.Fatalf("missing separator: %q", out)
	}
	out = captureOutSlash(t, env, "/mcp @nope:config://app")
	if !strings.Contains(out, "mcp 失败") || !strings.Contains(out, "没有 MCP 服务器") {
		t.Fatalf("unknown server: %q", out)
	}

	// 列表在没有服务器时给出双能力说明。
	if !strings.Contains(out, "提示词或资源") {
		t.Fatalf("list mentions both: %q", out)
	}
}

// TestWrapResourceFenceSurvivesMarkdown: 资源文本含行首裸 ``` 时围栏动
// 态加长（issue #178 同类——固定三反引号会让 Markdown 资源破栏）。
func TestWrapResourceFenceSurvivesMarkdown(t *testing.T) {
	md := "# 配置\n\n```yaml\nport: 1\n```\n"
	wrapped := wrapResource("config://app", md)
	if !strings.Contains(wrapped, "config://app") || !strings.Contains(wrapped, "port: 1") {
		t.Fatalf("content lost: %q", wrapped)
	}
	if !strings.Contains(wrapped, export.FenceFor(md)) {
		t.Fatalf("fence not dynamic for markdown resource:\n%s", wrapped)
	}
}

// TestSlashPerms 验证 /perms（spec 规则 36）：空会话说明用法语义；有规
// 则时列出工具与参数模式。
func TestSlashPerms(t *testing.T) {
	env, _ := newEnv(t)
	// 脚本 provider：关自动命名（其补全请求会吃掉脚本回合/竞态清理）。
	env.Svc.DisableAutoTitleForTesting()

	out := captureOutSlash(t, env, "/perms")
	if !strings.Contains(out, "还没有") {
		t.Fatalf("empty perms: %q", out)
	}

	// 经真实 ask 流程攒一条规则：SetAsker 回 Always。
	env.Svc.SetAsker(func(req agent.AskRequest) agent.AskResponse {
		return agent.AskResponse{Always: true, Note: req.Tool}
	})
	t.Cleanup(func() {
		env.Svc.SetAsker(func(agent.AskRequest) agent.AskResponse { return agent.AskResponse{} })
	})
	// 执行一个会触发权限询问的工具回合（bash 中风险必问）。
	out = chProviderTurn(t, env, "列一下")
	_ = out
	rules := env.Svc.SessionPermissionRules(env.SessionID())
	if len(rules) == 0 {
		t.Fatal("always-allow did not accumulate a rule")
	}
	out = captureOutSlash(t, env, "/perms")
	if !strings.Contains(out, "总是允许规则") || !strings.Contains(out, rules[0].Tool) {
		t.Fatalf("listing = %q", out)
	}
}

// chProviderTurn 跑一个带工具调用的回合（一次性通道）。
func chProviderTurn(t *testing.T, env *Env, prompt string) string {
	t.Helper()
	ch := make(chan ai.StreamEvent, 6)
	ch <- ai.StreamEvent{Kind: ai.EvMessageStart}
	ch <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "c1", ToolCallName: "bash"}
	ch <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "c1", ToolCallArgs: `{"command":"del /f x.txt"}`}
	ch <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "done"}
	ch <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(ch)
	env.Svc.UseProviderForTesting(func() ai.Provider { return chP{ch} })
	defer env.Svc.UseProviderForTesting(nil)
	var lines []string
	var lmu sync.Mutex // emit 可来自并行工具/收尾 goroutine——裸追加竞态（-race 逮过）
	if err := env.Svc.Chat(env.SessionID(), prompt, func(ev agent.Event) {
		lmu.Lock()
		lines = append(lines, string(ev.Kind))
		lmu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, ",")
}

type chP struct{ ch <-chan ai.StreamEvent }

func (p chP) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent { return p.ch }

// TestCustomCommands 验证自定义命令（spec 规则 39）：工作区与全局加载、
// 同名工作区覆盖、$ARGUMENTS 替换、builtin 不被遮蔽、/help 列出。
func TestCustomCommands(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	cmdDir := filepath.Join(ws, ".zgo", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "deploy.md"),
		[]byte("部署 $ARGUMENTS 到测试环境"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf(`{"model":"m"}`)
	_ = settings
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	cmds := svc.CustomCommands(ws)
	if len(cmds) != 1 || cmds[0].Name != "deploy" {
		t.Fatalf("cmds = %+v", cmds)
	}

	env, _ := newEnv(t)
	env.Workspace = func() string { return ws }
	var sent string
	env.Send = func(text string) { sent = text }

	// $ARGUMENTS 替换后发送。
	Run(env, "/deploy --canary")
	if sent != "部署 --canary 到测试环境" {
		t.Fatalf("sent = %q", sent)
	}

	// builtin 优先：/new 仍是内置。
	out := captureOutSlash(t, env, "/new")
	if !strings.Contains(out, "新建") {
		t.Fatalf("builtin shadowed: %q", out)
	}

	// /help 列出自定义段。
	out = captureOutSlash(t, env, "/help")
	if !strings.Contains(out, "自定义命令") || !strings.Contains(out, "deploy") {
		t.Fatalf("help missing custom: %q", out)
	}
}

// TestSlashSkills 验证 /skills（spec 规则 41）：无技能说明放置位置；工
// 作区技能列出名字与描述；坏技能的诊断以 ⚠ 行出现（跳过不拖垮列表）。
func TestSlashSkills(t *testing.T) {
	env, _ := newEnv(t)

	out := captureOutSlash(t, env, "/skills")
	if !strings.Contains(out, "未发现技能") || !strings.Contains(out, ".zgo/skills") {
		t.Fatalf("empty listing: %q", out)
	}

	ws := env.Workspace()
	dir := filepath.Join(ws, ".zgo", "skills", "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: deploy\ndescription: Ship the service\n---\n正文"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(ws, ".zgo", "skills", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "SKILL.md"),
		[]byte("---\ndescription: no name\n---\nx"), 0o644); err != nil {
		t.Fatal(err)
	}

	out = captureOutSlash(t, env, "/skills")
	if !strings.Contains(out, "deploy：Ship the service") {
		t.Fatalf("listing missing skill: %q", out)
	}
	if !strings.Contains(out, "⚠") || !strings.Contains(out, "must include a name") {
		t.Fatalf("diagnostic missing: %q", out)
	}
}

// TestSlashFindSurfaceHooks /find 的表面钩子分发：无钩子报不支持、
// 带钩子调用（TUI 跳转语义已在上面覆盖）。
func TestSlashFindSurfaceHooks(t *testing.T) {
	env, _ := newEnv(t)
	out := captureOutSlash(t, env, "/find 词")
	if !strings.Contains(out, "不支持") {
		t.Fatalf("no hook = %q", out)
	}
	env2, _ := newEnv(t)
	env2.Find = func(q string) string { return "找到 " + q }
	out = captureOutSlash(t, env2, "/find 目标")
	if !strings.Contains(out, "找到 目标") {
		t.Fatalf("hook out = %q", out)
	}
	if out := captureOutSlash(t, env2, "/find"); !strings.Contains(out, "用法") {
		t.Fatalf("empty = %q", out)
	}
}
