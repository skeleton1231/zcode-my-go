// Package slash is the shared slash-command table (ZCode's
// slash-commands): every surface — desktop composer, line REPL,
// full-screen TUI — dispatches the same commands through it.
package slash

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
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

	// Attach queues an image for the next message（终端表面的 /attach；
	// 桌面走自己的 📎，不填即不可用）.
	Attach func(path string) string
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
	if name == "/attach" {
		// 附件是表面状态（各终端攒 pending，随下一条消息发送）：由表面
		// 提供回调；未提供的表面明确报不支持。
		if env.Attach == nil {
			env.Out("/attach 在此表面不可用")
			return true, false
		}
		if args == "" {
			env.Out("用法：/attach <图片路径>")
			return true, false
		}
		env.Out(env.Attach(args))
		return true, false
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
		name: "/resume", usage: "/resume [前缀]",
		help: "切换会话（无参 = 当前工作区最近；前缀指定，见 zgo sessions list）",
		run: func(env *Env, args string) string {
			prefix := strings.TrimSpace(args)
			var info agent.SessionInfo
			if prefix != "" {
				id, ok := env.Svc.ResolveSession(prefix)
				if !ok {
					return "[恢复失败] 没有会话 " + strconv.Quote(prefix) +
						"（完整 ID 见 zgo sessions list；支持唯一前缀）"
				}
				for _, si := range env.Svc.ListSessions() {
					if si.ID == id {
						info = si
						break
					}
				}
			} else {
				ws := ""
				if env.Workspace != nil {
					ws = env.Workspace()
				}
				var ok bool
				info, ok = env.Svc.ContinueSession(ws)
				if !ok {
					return "[恢复失败] 当前工作区没有历史会话（zgo sessions list 查看；或 /resume <前缀>）"
				}
			}
			if info.ID == env.SessionID() {
				return "[恢复] 已在该会话"
			}
			if env.SetSession != nil {
				env.SetSession(info)
			}
			short := info.ID
			if len(short) > 8 {
				short = short[:8]
			}
			return "[恢复] 已切换到会话 " + short + " · " + info.Title
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
		name: "/export", usage: "/export [md] [路径]",
		help: "导出当前会话（默认 HTML；md 为 Markdown）",
		run: func(env *Env, args string) string {
			id := env.SessionID()
			asMD, dest := false, ""
			for _, a := range strings.Fields(args) {
				switch {
				case a == "md" || a == "markdown":
					asMD = true
				case a == "html":
					asMD = false
				default:
					dest = a
				}
			}
			if dest == "" {
				ext := ".html"
				if asMD {
					ext = ".md"
				}
				dest = "zgo-export-" + time.Now().Format("0102-1504") + ext
			}
			var err error
			if asMD {
				err = env.Svc.ExportSessionMarkdown(id, dest)
			} else {
				err = env.Svc.ExportSessionHTML(id, dest)
			}
			if err != nil {
				return "[导出失败] " + err.Error()
			}
			return "[导出] " + dest
		},
	},
	{
		name: "/rewind", usage: "/rewind [n]",
		help: "回滚到第 n 轮之前（默认最近一轮；含工作区文件）",
		run: func(env *Env, args string) string {
			n := 0
			if args != "" {
				v, err := strconv.Atoi(strings.TrimSpace(args))
				if err != nil || v < 1 {
					return "[用法] /rewind [n]（n 为 1 起的轮次）"
				}
				n = v
			}
			id := env.SessionID()
			if n == 0 {
				if err := env.Svc.RewindLatest(id); err != nil {
					return "[回滚失败] " + err.Error()
				}
				return "[回滚] 已撤销最近一轮（含工作区文件）"
			}
			if err := env.Svc.RewindTo(id, n); err != nil {
				return "[回滚失败] " + err.Error()
			}
			return fmt.Sprintf("[回滚] 已回到第 %d 轮之前（含工作区文件）", n)
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
			// 未识别要立刻可见（issue #61）：手滑拼写曾得到成功假象，
			// 下次 Chat 才 400。警告不阻断——README 承诺任意模型 ID，
			// 自定义端点的非目录模型是合法用法。
			if !ai.KnownModel(args) {
				return "[模型] 已切换为 " + args +
					"（未识别：不在目录中，按自定义模型使用；若拼写有误请重试）"
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
		name: "/login", usage: "/login",
		help: "订阅登录（浏览器 OAuth，Claude Pro/Max）",
		run: func(env *Env, _ string) string {
			ok, expires := env.Svc.LoginStatus()
			if ok {
				return fmt.Sprintf("已登录（%s 到期）；再次 /login 可重新授权", expires.Format("2006-01-02 15:04"))
			}
			done := env.Svc.LoginStart(func(link string) {
				env.Out("在浏览器完成授权：\n" + link + "\n（回调服务器已就绪，最长等待 5 分钟）")
			})
			go func() {
				if err := <-done; err != nil {
					env.Out("登录失败：" + err.Error())
					return
				}
				env.Out("登录成功，订阅凭据已保存")
			}()
			return "[登录] 已启动 OAuth 流程"
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
	// 退出拼写各自成条：合并成 "/quit /q" 会让输入 "/q" 时前缀匹配
	// 落空、面板不弹。
	for _, alias := range []string{"/exit", "/quit", "/q"} {
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
