package main

// The terminal surfaces share internal/slash's command table; this file
// only adapts it to the REPL/TUI environment.

import (
	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/slash"
)

// commandEnv carries what the terminal commands act on.
type commandEnv struct {
	svc       *service.AgentService
	info      *agent.SessionInfo
	workspace string
	out       func(string)
	attach    *attachments // 终端表面的 /attach 状态（可空）
}

// runSlashCommand adapts slash.Run for the terminal surfaces.
func runSlashCommand(env *commandEnv, line string) (handled, quit bool) {
	var attachFn func(string) string
	if env.attach != nil {
		attachFn = env.attach.add
	}
	return slash.Run(&slash.Env{
		Svc:        env.svc,
		SessionID:  func() string { return env.info.ID },
		Workspace:  func() string { return env.workspace },
		SetSession: func(info agent.SessionInfo) { *env.info = info },
		// 输出只经 env.out：REPL 打 stderr、TUI 走 push。适配器再打
		// 一份 stderr 会让 REPL 双行输出、TUI 全屏被裸文本污染。
		Out:    env.out,
		Attach: attachFn,
	}, line)
}

// maybeSteer 运行中把消息入队为插话（spec 规则 26，与桌面对齐）：下
// 一回合生效、不打断当前流。空闲会话返回 false——调用方走常规发送。
func maybeSteer(svc *service.AgentService, sessionID, text string) (steered bool, notice string) {
	if !svc.SessionRunning(sessionID) {
		return false, ""
	}
	if err := svc.Steer(sessionID, text); err != nil {
		return true, "[插话失败] " + err.Error()
	}
	return true, "[插话] 已入队，当前回合结束后生效"
}
