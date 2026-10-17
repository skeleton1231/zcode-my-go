// Package slash is the shared slash-command table (ZCode's
// slash-commands): every surface — desktop composer, line REPL,
// full-screen TUI — dispatches the same commands through it.
package slash

import (
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// Env carries what a command may act on and how it reports back.
type Env struct {
	Svc *service.AgentService
	// SessionID returns the active session (commands like /new replace
	// it through SetSession).
	SessionID func() string
	// Workspace seeds /new when it must create a session itself.
	Workspace func() string
	// SetSession receives the new session /new created (surfaces open it).
	SetSession func(agent.SessionInfo)
	// Out receives command feedback.
	Out func(string)
}

// Run dispatches one line; handled reports whether it was a command,
// quit whether an exit spelling asked to stop. Chat lines fall through.
func Run(env *Env, line string) (handled, quit bool) {
	if !strings.HasPrefix(line, "/") {
		return false, false
	}
	name, args, _ := strings.Cut(line, " ")
	name = strings.ToLower(name)
	if quitAliases[name] {
		return true, true
	}
	if name == "/help" {
		env.Out(helpText())
		return true, false
	}
	for _, c := range commands {
		if c.name != name {
			continue
		}
		env.Out(c.run(env, strings.TrimSpace(args)))
		return true, false
	}
	env.Out("未知命令 " + name + "，/help 查看全部")
	return true, false
}

// quitAliases are the exit spellings.
var quitAliases = map[string]bool{"/exit": true, "/quit": true, "/q": true}

type command struct {
	name  string
	usage string
	help  string
	run   func(env *Env, args string) string
}

var commands = []command{
	{
		name: "/new", usage: "/new",
		help: "开启新会话",
		run: func(env *Env, _ string) string {
			ws := ""
			if env.Workspace != nil {
				ws = env.Workspace()
			}
			info, err := env.Svc.NewSession(ws)
			if err != nil {
				return err.Error()
			}
			if env.SetSession != nil {
				env.SetSession(info)
			}
			return "[新建] 已开启新会话"
		},
	},
	{
		name: "/compact", usage: "/compact",
		help: "压缩会话（摘要 + 保留最近对话）",
		run: func(env *Env, _ string) string {
			if err := env.Svc.CompactSession(env.SessionID()); err != nil {
				return "[压缩失败] " + err.Error()
			}
			return "[压缩] 会话已压缩"
		},
	},
	{
		name: "/plan", usage: "/plan [on|off]",
		help: "切换计划模式（只读）",
		run: func(env *Env, args string) string {
			id := env.SessionID()
			on := !env.Svc.PlanModeState(id)
			switch args {
			case "on":
				on = true
			case "off":
				on = false
			}
			if err := env.Svc.PlanMode(id, on); err != nil {
				return err.Error()
			}
			if on {
				return "[计划模式] 已开启（只读）"
			}
			return "[计划模式] 已关闭"
		},
	},
	{
		name: "/model", usage: "/model <模型ID>",
		help: "切换模型（无参显示当前）",
		run: func(env *Env, args string) string {
			v := env.Svc.GetSettings()
			if args == "" {
				return fmt.Sprintf("当前：%s（协议 %s，思考 %s）；用法：/model <ID>",
					orDash(v.Model), v.API, orDash(v.Thinking))
			}
			if _, err := env.Svc.SaveSettings(v.API, v.BaseURL, "", args, v.Thinking); err != nil {
				return err.Error()
			}
			return "[模型] 已切换为 " + args
		},
	},
	{
		name: "/rename", usage: "/rename <标题>",
		help: "重命名会话",
		run: func(env *Env, args string) string {
			if args == "" {
				return "用法：/rename <标题>"
			}
			if err := env.Svc.RenameSession(env.SessionID(), args); err != nil {
				return err.Error()
			}
			return "[重命名] " + args
		},
	},
	{
		name: "/status", usage: "/status",
		help: "模型、思考等级、用量与上下文预算",
		run: func(env *Env, _ string) string {
			v := env.Svc.GetSettings()
			var b strings.Builder
			fmt.Fprintf(&b, "模型 %s · 协议 %s · 思考 %s\n", orDash(v.Model), v.API, orDash(v.Thinking))
			if total, err := env.Svc.SessionUsage(env.SessionID()); err == nil {
				fmt.Fprintf(&b, "用量 累计 ↑%d ↓%d tokens\n",
					total.InputTokens+total.CacheReadTokens+total.CacheWriteTokens, total.OutputTokens)
			}
			if budget, err := env.Svc.ContextBudget(env.SessionID()); err == nil && budget.Window > 0 {
				pct := 100 * budget.Used / budget.Window
				fmt.Fprintf(&b, "上下文 %d/%d（%d%%，压缩阈值 %d）",
					budget.Used, budget.Window, pct, budget.Threshold)
			}
			return strings.TrimRight(b.String(), "\n")
		},
	},
}

func helpText() string {
	var b strings.Builder
	b.WriteString("命令：\n")
	b.WriteString("  /help                       列出全部命令\n")
	b.WriteString("  /exit /quit /q              退出\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-28s %s\n", c.usage, c.help)
	}
	return strings.TrimRight(b.String(), "\n")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Command is a table entry exported for surfaces that hint at commands
// (the desktop composer's autocomplete panel).
type Command struct {
	Usage string
	Help  string
}

// Commands lists every command's usage and help, /help and the exit
// spellings included.
func Commands() []Command {
	out := []Command{{Usage: "/help", Help: "列出全部命令"}}
	for _, alias := range []string{"/exit", "/quit /q"} {
		out = append(out, Command{Usage: alias, Help: "退出"})
	}
	for _, c := range commands {
		out = append(out, Command{Usage: c.usage, Help: c.help})
	}
	return out
}

// Match returns the commands whose usage starts with the query (a
// "/"-prefix matches everything), capped for a hint panel.
func Match(query string) []Command {
	query = strings.ToLower(strings.TrimSpace(query))
	if !strings.HasPrefix(query, "/") {
		return nil
	}
	var out []Command
	for _, c := range Commands() {
		usage := strings.SplitN(c.Usage, " ", 2)[0]
		if strings.HasPrefix(usage, query) || strings.HasPrefix(query, usage) {
			out = append(out, c)
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}
