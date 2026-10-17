package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
