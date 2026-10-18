package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
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

	// 计划模式显式 on/off（状态由 service 管理）。
	runSlashCommand(env, "/plan on")
	if !env.svc.PlanModeState(env.info.ID) {
		t.Fatal("plan on not applied")
	}
	runSlashCommand(env, "/plan off")
	if env.svc.PlanModeState(env.info.ID) {
		t.Fatal("plan off not applied")
	}
}

// TestMaybeSteer 验证终端插话路由（spec 规则 26）：运行中入队并给提
// 示、空闲返回 false 走常规发送。运行态用阻塞 provider 维持（等 ctx
// 取消才结束）。
func TestMaybeSteer(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.SetAsker(func(agent.AskRequest) agent.AskResponse { return agent.AskResponse{} })
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 空闲：常规发送路径。
	if steered, _ := maybeSteer(svc, info.ID, "空闲时"); steered {
		t.Fatal("idle session must not steer")
	}

	// 运行中：阻塞到 ctx 取消的 provider 撑住回合。
	blocked := make(chan struct{})
	svc.UseProviderForTesting(func() ai.Provider {
		return blockProvider{done: blocked}
	})
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })
	go func() { _ = svc.Chat(info.ID, "长任务", func(agent.Event) {}) }()
	waitFor(t, func() bool { return svc.SessionRunning(info.ID) }, 5*time.Second, "run to start")

	steered, notice := maybeSteer(svc, info.ID, "先看看测试")
	if !steered || !strings.Contains(notice, "已入队") {
		t.Fatalf("steered=%v notice=%q", steered, notice)
	}

	close(blocked) // 解除回合
	waitFor(t, func() bool { return !svc.SessionRunning(info.ID) }, 5*time.Second, "run to end")

	// 插话随下一回合注入：转录里出现该用户消息。
	found := false
	if msgs, err := svc.Messages(info.ID); err == nil {
		for _, m := range msgs {
			if m.Text() == "先看看测试" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("steered message never entered the transcript")
	}
}

// blockProvider 挂起流直到 done 关闭（维持运行态）。
type blockProvider struct{ done chan struct{} }

func (p blockProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "工作ing"}
	go func() {
		select {
		case <-p.done:
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
			close(out)
		case <-ctx.Done():
			close(out)
		}
	}()
	return out
}

func waitFor(t *testing.T, cond func() bool, d time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
