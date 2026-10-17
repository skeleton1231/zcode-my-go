package main

// Shared slash commands for the terminal surfaces (ZCode's
// slash-commands): one table the line-mode REPL and the full-screen TUI
// both dispatch through.

import (
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// commandEnv carries what a command may act on.
type commandEnv struct {
	svc       *service.AgentService
	info      *agent.SessionInfo
	workspace string
	// out receives command feedback (not chat text).
	out func(string)
}

// command is one slash command.
type command struct {
	name  string
	usage string
	help  string
	// run returns an error message to print, or "" on success.
	run func(env *commandEnv, args string) string
}

var slashCommands = []command{
	{
		name: "/exit", usage: "/exit",
		help: "退出（等同 /quit /q）",
		run:  nil, // 退出语义由调用方处理
	},
	{
		name: "/new", usage: "/new",
		help: "开启新会话",
		run: func(env *commandEnv, _ string) string {
			fresh, err := env.svc.NewSession(env.workspace)
			if err != nil {
				return err.Error()
			}
			*env.info = fresh
			return "[新建] 已开启新会话"
		},
	},
	{
		name: "/compact", usage: "/compact",
		help: "压缩会话（摘要 + 保留最近对话）",
		run: func(env *commandEnv, _ string) string {
			if err := env.svc.CompactSession(env.info.ID); err != nil {
				return "[压缩失败] " + err.Error()
			}
			return "[压缩] 会话已压缩"
		},
	},
	{
		name: "/plan", usage: "/plan [on|off]",
		help: "切换计划模式（只读）",
		run: func(env *commandEnv, args string) string {
			on := !planState[env.info.ID]
			switch strings.TrimSpace(args) {
			case "on":
				on = true
			case "off":
				on = false
			}
			if err := env.svc.PlanMode(env.info.ID, on); err != nil {
				return err.Error()
			}
			planState[env.info.ID] = on
			if on {
				return "[计划模式] 已开启（只读）"
			}
			return "[计划模式] 已关闭"
		},
	},
	{
		name: "/model", usage: "/model <模型ID>",
		help: "切换模型（如 glm-5.2 / gpt-5.2 / gemini-3-pro）",
		run: func(env *commandEnv, args string) string {
			id := strings.TrimSpace(args)
			if id == "" {
				v := env.svc.GetSettings()
				return fmt.Sprintf("当前：%s（协议 %s，思考 %s）；用法：/model <ID>",
					or(v.Model, "未配置"), v.API, or(v.Thinking, "off"))
			}
			v := env.svc.GetSettings()
			if _, err := env.svc.SaveSettings(v.API, v.BaseURL, "", id, v.Thinking); err != nil {
				return err.Error()
			}
			return "[模型] 已切换为 " + id
		},
	},
	{
		name: "/rename", usage: "/rename <标题>",
		help: "重命名会话",
		run: func(env *commandEnv, args string) string {
			title := strings.TrimSpace(args)
			if title == "" {
				return "用法：/rename <标题>"
			}
			if err := env.svc.RenameSession(env.info.ID, title); err != nil {
				return err.Error()
			}
			return "[重命名] " + title
		},
	},
	{
		name: "/status", usage: "/status",
		help: "模型、思考等级、用量与上下文预算",
		run: func(env *commandEnv, _ string) string {
			v := env.svc.GetSettings()
			var b strings.Builder
			fmt.Fprintf(&b, "模型 %s · 协议 %s · 思考 %s\n", or(v.Model, "未配置"), v.API, or(v.Thinking, "off"))
			if total, err := env.svc.SessionUsage(env.info.ID); err == nil {
				fmt.Fprintf(&b, "用量 累计 ↑%d ↓%d tokens\n",
					total.InputTokens+total.CacheReadTokens+total.CacheWriteTokens, total.OutputTokens)
			}
			if budget, err := env.svc.ContextBudget(env.info.ID); err == nil && budget.Window > 0 {
				pct := 100 * budget.Used / budget.Window
				fmt.Fprintf(&b, "上下文 %d/%d（%d%%，压缩阈值 %d）\n",
					budget.Used, budget.Window, pct, budget.Threshold)
			}
			return strings.TrimRight(b.String(), "\n")
		},
	},
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// quitAliases are the exit spellings.
var quitAliases = map[string]bool{"/exit": true, "/quit": true, "/q": true}

// runSlashCommand dispatches one line; handled reports whether it was a
// command, quit whether an exit spelling asked to stop. Chat lines fall
// through.
func runSlashCommand(env *commandEnv, line string) (handled, quit bool) {
	if !strings.HasPrefix(line, "/") {
		return false, false
	}
	name, args, _ := strings.Cut(line, " ")
	name = strings.ToLower(name)
	if quitAliases[name] {
		return true, true
	}
	if name == "/help" {
		var b strings.Builder
		b.WriteString("命令：\n")
		for _, c := range slashCommands {
			fmt.Fprintf(&b, "  %-28s %s\n", c.usage, c.help)
		}
		env.out(strings.TrimRight(b.String(), "\n"))
		return true, false
	}
	for _, c := range slashCommands {
		if c.name != name {
			continue
		}
		env.out(c.run(env, args))
		return true, false
	}
	env.out("未知命令 " + name + "，/help 查看全部")
	return true, false
}
