package slash

import (
	"strings"
	"testing"

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
