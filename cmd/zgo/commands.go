package main

// The terminal surfaces share internal/slash's command table; this file
// only adapts it to the REPL/TUI environment.

import (
	"fmt"
	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/slash"
	"io"
)

// commandEnv carries what the terminal commands act on.
type commandEnv struct {
	svc       *service.AgentService
	info      *agent.SessionInfo
	workspace string
	out       func(string)
	attach    *attachments        // 终端表面的 /attach 状态（可空）
	send      func(string)        // /mcp 展开后直接发送（可空即不支持）
	rerun     func(string) error  // /retry、/resend 的 EditAndResend 包装（可空即不支持）
	find      func(string) string // /find 的表面实现（TUI 跳转标记/REPL 列清单；可空即不支持）
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
		Send:   env.send,
		Rerun:  env.rerun,
		Find:   env.find,
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

// trustHint 未授信工作区的表面提示（spec 规则 53）：规则/命令/技能层
// 未加载；授信途径 /trust 或 zgo trust <路径>（print 无法交互，提示走
// stderr）。
func trustHint(w io.Writer, svc *service.AgentService, workspace string) {
	if svc.IsWorkspaceTrusted(workspace) {
		return
	}
	fmt.Fprintf(w, "[信任] 工作区未授信，其 .zgorules.json 与 .zgo 命令/技能层未加载——/trust 授信（或 zgo trust %s）\n", workspace)
}
