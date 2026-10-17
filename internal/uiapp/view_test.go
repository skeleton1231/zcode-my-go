package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// waitIdle waits for the run Send started to end; the tester serializes
// frames, so this only works without a window attached.
func waitIdle(t *testing.T, a *App) {
	t.Helper()
	for range 500 {
		if !a.sending && !a.chat.running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run never finished")
}

// TestViewSmoke runs the whole shell without a window: open a session, send
// a message, and watch the (unconfigured, thus erroring) run end cleanly.
func TestViewSmoke(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	info, err := svc.NewSession(workspace)
	if err != nil {
		t.Fatal(err)
	}

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(info.ID)
	tt.Frame()

	if !tt.HasText("把任务交给 agent") {
		t.Fatalf("empty-state text missing: %q", tt.Texts())
	}

	a.input = "hello"
	tt.Frame()
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("clicking send: %v", err)
	}
	waitIdle(t, a)
	tt.Frame()

	if !tt.HasText("hello") {
		t.Fatalf("user message missing: %q", tt.Texts())
	}
	if a.chat.endErr == "" {
		t.Fatalf("expected an error end without an API key, got %+v", a.chat)
	}
	if a.chat.running || a.sending {
		t.Fatalf("still running: %+v", a)
	}
}

// TestSettingsModal opens the modal and saves a model id.
func TestSettingsModal(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()

	a.OpenSettings()
	tt.Frame()
	if !tt.HasText("API Base URL") {
		t.Fatalf("settings fields missing: %q", tt.Texts())
	}

	a.setModel = "glm-5.2"
	a.setBaseURL = "https://open.bigmodel.cn/api/anthropic"
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("clicking save: %v", err)
	}
	for range 500 {
		if !a.saving {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tt.Frame()

	if a.settings == nil || a.settings.Model != "glm-5.2" || a.settings.HasAPIKey {
		t.Fatalf("settings after save: %+v", a.settings)
	}
	if a.settingsOpen {
		t.Fatal("modal still open after save")
	}
	if !tt.HasText("glm-5.2") {
		t.Fatalf("model not shown in the status bar: %q", tt.Texts())
	}
}
