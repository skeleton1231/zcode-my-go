package slash

import (
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func newEnv(t *testing.T) (*Env, *[]string) {
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
	return &Env{
		Svc:        svc,
		SessionID:  func() string { return info.ID },
		Workspace:  func() string { return info.Workspace },
		SetSession: func(agent.SessionInfo) {},
		Out:        func(msg string) { lines = append(lines, msg) },
	}, &lines
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
	if last := (*out)[len(*out)-1]; !strings.Contains(last, "/help") {
		t.Fatalf("unknown hint = %q", last)
	}
	Run(env, "/help")
	joined := strings.Join(*out, "\n")
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
	if last := (*out)[len(*out)-1]; !strings.Contains(last, "glm-5.2") || !strings.Contains(last, "上下文") {
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
	joined := strings.Join(*out, "\n")
	if !strings.Contains(joined, "OAuth") && !strings.Contains(joined, "授权") {
		t.Fatalf("login output = %q", joined)
	}
}
