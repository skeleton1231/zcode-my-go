package main

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func newCommandEnv(t *testing.T) (*commandEnv, *[]string) {
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
	env := &commandEnv{
		svc: svc, info: &info, workspace: t.TempDir(),
		out: func(msg string) { lines = append(lines, msg) },
	}
	return env, &lines
}

func TestSlashCommandTable(t *testing.T) {
	env, out := newCommandEnv(t)

	// 非命令透传。
	if handled, quit := runSlashCommand(env, "普通问题"); handled || quit {
		t.Fatal("chat line swallowed")
	}
	// 退出拼写。
	for _, q := range []string{"/exit", "/quit", "/q", "/EXIT"} {
		if _, quit := runSlashCommand(env, q); !quit {
			t.Fatalf("%s did not quit", q)
		}
	}
	// 未知命令提示帮助。
	runSlashCommand(env, "/nope")
	if len(*out) == 0 || !strings.Contains((*out)[len(*out)-1], "/help") {
		t.Fatalf("unknown hint = %v", *out)
	}
	// /help 列出全部命令。
	runSlashCommand(env, "/help")
	joined := strings.Join(*out, "\n")
	for _, name := range []string{"/new", "/compact", "/plan", "/model", "/rename", "/status"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("/help missing %s", name)
		}
	}
}

func TestSlashModelAndRenameAndStatus(t *testing.T) {
	env, out := newCommandEnv(t)

	runSlashCommand(env, "/model glm-5.2")
	if v := env.svc.GetSettings(); v.Model != "glm-5.2" {
		t.Fatalf("model = %q", v.Model)
	}
	// 无参 /model 报当前值。
	runSlashCommand(env, "/model")
	last := (*out)[len(*out)-1]
	if !strings.Contains(last, "glm-5.2") {
		t.Fatalf("status line = %q", last)
	}

	runSlashCommand(env, "/rename 终端调试")
	found := false
	for _, s := range env.svc.ListSessions() {
		if s.ID == env.info.ID && s.Title == "终端调试" {
			found = true
		}
	}
	if !found {
		t.Fatal("rename not applied")
	}

	runSlashCommand(env, "/status")
	last = (*out)[len(*out)-1]
	if !strings.Contains(last, "glm-5.2") || !strings.Contains(last, "上下文") {
		t.Fatalf("status = %q", last)
	}

	// 计划模式显式 on/off。
	runSlashCommand(env, "/plan on")
	if !planState[env.info.ID] {
		t.Fatal("plan on not tracked")
	}
	runSlashCommand(env, "/plan off")
	if planState[env.info.ID] {
		t.Fatal("plan off not tracked")
	}
}
