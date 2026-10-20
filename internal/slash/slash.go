// Package slash is the shared slash-command table (ZCode's
// slash-commands): every surface — desktop composer, line REPL,
// full-screen TUI — dispatches the same commands through it.
package slash

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/export"
	"github.com/skeleton1231/zcode-my-go/internal/mcp"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
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
	// Send submits a message as if the user typed it（/mcp 展开后直接
	// 发送；不填即该表面不支持直接发送）.
	Send func(text string)
	// Find searches the current session transcript（spec 规则 57：TUI
	// 返回跳转状态并进入标记模式，REPL 返回命中清单；不填即不支持）.
	Find func(q string) string

	// Rerun replaces the last user message and reruns the turn
	//（/retry 与 /resend；EditAndResend 的表面包装）.
	Rerun func(text string) error
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
		env.Out(helpText() + customCommandsHelp(env))
		return true, false
	}
	for _, c := range commands {
		if c.name != name {
			continue
		}
		env.Out(c.run(env, strings.TrimSpace(args)))
		return true, false
	}
	// 自定义命令（spec 规则 39）：builtin 优先，同名不遮蔽（名字补斜杠
	// 对齐 builtin 的 "/name" 形态）。
	for _, cc := range env.Svc.CustomCommands(env.Workspace()) {
		if "/"+cc.Name != name {
			continue
		}
		text := strings.ReplaceAll(cc.Text, "$ARGUMENTS", strings.TrimSpace(args))
		if env.Send == nil {
			env.Out("[自定义命令 /" + cc.Name + " 展开如下（此表面不能直接发送）：\n" + text)
			return true, false
		}
		env.Send(text)
		env.Out("[自定义命令] 已发送 /" + cc.Name)
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
		name: "/mcp", usage: "/mcp [server.prompt [k=v…]]",
		help: "MCP 提示词：列出或展开为消息直接发送",
		run: func(env *Env, args string) string {
			args = strings.TrimSpace(args)
			switch {
			case args == "":
				return mcpList(env)
			case strings.HasPrefix(args, "add "):
				return mcpAdd(env, strings.TrimSpace(strings.TrimPrefix(args, "add ")))
			case args == "remove" || strings.HasPrefix(args, "remove "):
				return mcpRemove(env, strings.TrimSpace(strings.TrimPrefix(args, "remove")))
			default:
				return mcpRun(env, args)
			}
		},
	},
	{
		name: "/perms", usage: "/perms [export|why <工具> [参数 JSON]]",
		help: "本会话累计的「总是允许」权限规则；export 固化成 .zgorules.json；why 预演一次调用的判定",
		run: func(env *Env, args string) string {
			if strings.TrimSpace(args) == "export" {
				return permsExport(env)
			}
			if rest, ok := strings.CutPrefix(strings.TrimSpace(args), "why"); ok {
				// why <工具> [参数 JSON]：判定预演（spec 规则 129）。
				fields := strings.Fields(rest)
				if len(fields) == 0 {
					return "用法：/perms why <工具> [参数 JSON]（例：/perms why webfetch {\"url\":\"https://go.dev\"}）"
				}
				tool := fields[0]
				raw := json.RawMessage(nil)
				if len(fields) > 1 {
					raw = json.RawMessage(strings.Join(fields[1:], " "))
					if !json.Valid(raw) {
						return "参数不是合法 JSON：" + string(raw)
					}
				}
				why, err := env.Svc.PermWhy(env.SessionID(), tool, raw)
				if err != nil {
					return err.Error()
				}
				return "[判定预演] " + why
			}
			rules := env.Svc.SessionPermissionRules(env.SessionID())
			if len(rules) == 0 {
				return "[权限] 本会话还没有「总是允许」规则——权限询问时回答 a（总是允许）会按工具/路径积累，随会话结束消散"
			}
			var b strings.Builder
			b.WriteString("[权限] 本会话的总是允许规则：\n")
			for _, r := range rules {
				line := "  " + r.Tool
				if r.Args != "" {
					line += " " + r.Args
				}
				b.WriteString(line + "\n")
			}
			return strings.TrimRight(b.String(), "\n")
		},
	},
	{
		name: "/trust", usage: "/trust",
		help: "授信当前工作区（加载其 .zgorules.json 与命令/技能层）",
		run: func(env *Env, _ string) string {
			ws := env.Workspace()
			if env.Svc.IsWorkspaceTrusted(ws) {
				return "[信任] 工作区已授信：" + ws
			}
			if err := env.Svc.TrustSession(env.SessionID(), "slash"); err != nil {
				return "[信任失败] " + err.Error()
			}
			return "[信任] 已授信并加载工作区规则与命令/技能层：" + ws
		},
	},
	{
		name: "/skills", usage: "/skills",
		help: "列出可用技能（工作区 .zgo/skills 与全局 ~/.zgo/skills）",
		run: func(env *Env, _ string) string {
			out := env.Svc.Skills(env.Workspace())
			if len(out.Skills) == 0 && len(out.Diagnostics) == 0 {
				return "[技能] 未发现技能——放 <工作区>/.zgo/skills/<名>/SKILL.md（frontmatter：name/description）或 ~/.zgo/skills/<名>/SKILL.md；同名工作区覆盖全局"
			}
			var b strings.Builder
			b.WriteString("[技能] 可用技能（skill 工具按名加载）：\n")
			for _, s := range out.Skills {
				line := "  " + s.Name
				if s.Description != "" {
					line += "：" + s.Description
				}
				b.WriteString(line + "\n")
			}
			for _, d := range out.Diagnostics {
				b.WriteString("  ⚠ " + d.Path + ": " + d.Message + "\n")
			}
			return strings.TrimRight(b.String(), "\n")
		},
	},
	{
		name: "/tools", usage: "/tools",
		help: "列出本会话的模型可见工具面（内置/自定义/MCP；spec 规则 77）",
		run: func(env *Env, _ string) string {
			tools := env.Svc.ToolsFor(env.SessionID(), env.Workspace())
			var b strings.Builder
			if len(tools) == 0 {
				return "[工具] 工具面不可用（会话未建立）"
			}
			b.WriteString("[工具] 本会话模型可见工具面：\n")
			cur := ""
			for _, t := range tools {
				if t.Source != cur { // 来源分组（builtin 在前、custom/mcp 随装配序）
					cur = t.Source
					switch cur {
					case "custom":
						b.WriteString("  自定义（.zgo/tools）：\n")
					case "mcp":
						b.WriteString("  MCP 服务器：\n")
					}
				}
				line := "  " + t.Name
				if t.Description != "" {
					line += "：" + t.Description
				}
				b.WriteString(line + "\n")
			}
			for _, d := range env.Svc.ToolDefDiagnostics(env.SessionID(), env.Workspace()) {
				b.WriteString("  ⚠ " + d.Path + ": " + d.Message + "\n")
			}
			return strings.TrimRight(b.String(), "\n")
		},
	},
	{
		name: "/context", usage: "/context",
		help: "显示上下文构成（系统提示/记忆/技能/工具声明/转录各占多少——spec 规则 79）",
		run: func(env *Env, _ string) string {
			parts := env.Svc.ContextBreakdown(env.SessionID(), env.Workspace())
			if len(parts) == 0 {
				return "[上下文] 会话未建立"
			}
			var b strings.Builder
			total := 0
			b.WriteString("[上下文] 下一请求的构成（字节口径，token 估算 ≈/4）：\n")
			for _, p := range parts {
				total += p.Bytes
				line := "  " + p.Name + "\t" + fmt.Sprintf("%6d B", p.Bytes)
				if p.Note != "" {
					line += "\t" + p.Note
				}
				b.WriteString(line + "\n")
			}
			b.WriteString("  合计\t" + fmt.Sprintf("%6d B", total) + "\t（/compact 压缩转录；/context 随时复测）")
			return b.String()
		},
	},
	{
		name: "/agents", usage: "/agents",
		help: "列出本会话的后台子代理（状态/耗时/任务）",
		run: func(env *Env, _ string) string {
			return FormatAgents(env.Svc.AgentJobStates(env.SessionID()), 10)
		},
	},
	{
		name: "/find", usage: "/find <词>",
		help: "在当前会话转录中搜索（TUI 跳转标记 / REPL 列命中）",
		run: func(env *Env, args string) string {
			q := strings.TrimSpace(args)
			if q == "" {
				return "[搜索] 用法：/find <词>"
			}
			if env.Find == nil {
				return "[搜索] 此表面不支持会话内搜索（TUI 支持）"
			}
			return env.Find(q)
		},
	},
	{
		name: "/usage", usage: "/usage",
		help: "本会话的 token 用量（输入/输出/缓存）",
		run: func(env *Env, _ string) string {
			u, err := env.Svc.UsageSummaryOf(env.SessionID())
			if err != nil {
				return "[用量失败] " + err.Error()
			}
			return "[用量]\n" + service.FormatUsage(u)
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
		name: "/retry", usage: "/retry",
		help: "重跑最后一轮（撤当前轮含工作区后原样重发）",
		run: func(env *Env, _ string) string {
			text, err := env.Svc.LastUserText(env.SessionID())
			if err != nil {
				return "[重跑失败] " + err.Error()
			}
			if text == "" {
				return "[重跑失败] 没有可重跑的回合"
			}
			return rerunTurn(env, text)
		},
	},
	{
		name: "/resend", usage: "/resend <新文本>",
		help: "编辑重发：撤最后一轮并以新文本重发",
		run: func(env *Env, args string) string {
			if strings.TrimSpace(args) == "" {
				return "[用法] /resend <新文本>（撤最后一轮并重发）"
			}
			return rerunTurn(env, strings.TrimSpace(args))
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
		name: "/provider", usage: "/provider [add] <名>",
		help: "切换 API 剖面（无参列出；add 注册当前配置为剖面）",
		run: func(env *Env, args string) string {
			args = strings.TrimSpace(args)
			if args == "" {
				v := env.Svc.GetSettings()
				var b strings.Builder
				b.WriteString("当前剖面：" + orDash(v.Profile) + "；可用：")
				for _, n := range v.Profiles {
					if n == v.Profile {
						n = "→ " + n
					}
					b.WriteString(n + " ")
				}
				return strings.TrimRight(b.String(), " ") +
					"；用法：/provider <名> 切换，/provider add <名> 注册当前配置"
			}
			if name, ok := strings.CutPrefix(args, "add "); ok {
				name = strings.TrimSpace(name)
				if err := env.Svc.AddProfile(name); err != nil {
					return err.Error()
				}
				return "[剖面] 已注册 " + name + "（快照当前配置）；/provider " + name + " 切换"
			}
			v, err := env.Svc.SwitchProfile(args)
			if err != nil {
				return err.Error()
			}
			return "[剖面] 已切换 " + v.Profile + "（模型 " + orDash(v.Model) +
				" @ " + orDash(hostOf(v.BaseURL)) + "）"
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
		name: "/add-dir", usage: "/add-dir <路径>",
		help: "附加工作目录——并入本会话的文件访问边界（spec 规则 128）",
		run: func(env *Env, args string) string {
			if args == "" {
				return "用法：/add-dir <绝对路径或相对当前目录的路径>（最多 8 个）"
			}
			dirs, err := env.Svc.AddWorkspaceDir(env.SessionID(), args)
			if err != nil {
				return err.Error()
			}
			return fmt.Sprintf("[add-dir] 已并入，当前根 %d 个", len(dirs))
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

// customCommandsHelp 附自定义命令段（spec 规则 39；/help 调用）。
func customCommandsHelp(env *Env) string {
	cmds := env.Svc.CustomCommands(env.Workspace())
	if len(cmds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n自定义命令（.zgo/commands/）：\n")
	for _, c := range cmds {
		first := strings.SplitN(strings.TrimSpace(c.Text), "\n", 2)[0]
		if r := []rune(first); len(r) > 24 {
			first = string(r[:24]) + "…"
		}
		fmt.Fprintf(&b, "  /%-26s %s\n", c.Name, first)
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

// mcpList renders every server's prompts and resources（spec 规则
// 28/31：提示词按 server.prompt 执行，资源按 @server:uri 读取）.
func mcpList(env *Env) string {
	entries := env.Svc.MCPPrompts()
	resources := env.Svc.MCPResources()
	if len(entries) == 0 && len(resources) == 0 {
		return "[mcp] 没有可用的提示词或资源（服务器未声明或未运行）"
	}
	var b strings.Builder
	b.WriteString("[mcp] 可用提示词：\n")
	for _, e := range entries {
		args := ""
		for _, a := range e.Prompt.Arguments {
			mark := ""
			if a.Required {
				mark = "*"
			}
			args += " " + a.Name + mark
		}
		line := e.Server + "." + e.Prompt.Name
		if e.Prompt.Description != "" {
			line += " — " + e.Prompt.Description
		}
		if args != "" {
			line += "（参数:" + args + "）"
		}
		b.WriteString("  " + line + "\n")
	}
	if len(resources) > 0 {
		b.WriteString("\n[mcp] 可用资源（@server:uri 读取）：\n")
		for _, e := range resources {
			line := "  @" + e.Server + ":" + e.Resource.URI
			if e.Resource.Description != "" {
				line += " — " + e.Resource.Description
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// mcpRun expands and sends one prompt: /mcp server.prompt k=v…
func mcpRun(env *Env, args string) string {
	target, rest, _ := strings.Cut(args, " ")
	if strings.HasPrefix(target, "@") {
		return mcpReadResource(env, strings.TrimPrefix(target, "@"))
	}
	server, prompt, found := strings.Cut(target, ".")
	if !found || server == "" || prompt == "" {
		return "[用法] /mcp <server>.<prompt> [k=v…]（/mcp 查看全部）"
	}
	kv := map[string]string{}
	for _, pair := range strings.Fields(rest) {
		if k, v, ok := strings.Cut(pair, "="); ok && k != "" {
			kv[k] = v
		}
	}
	text, err := env.Svc.MCPGetPrompt(server, prompt, kv)
	if err != nil {
		return "[mcp 失败] " + err.Error() + "\n" + mcpList(env)
	}
	if env.Send == nil {
		return "[mcp 不支持] 此表面不能直接发送，展开结果：\n" + text
	}
	env.Send(text)
	return "[mcp] 已发送 " + target
}

// rerunTurn 经表面 Rerun 钩子重跑最后一轮（spec 规则 29）：EditAnd
// Resend 的表面包装（运行守卫先于文件恢复）。
func rerunTurn(env *Env, text string) string {
	if env.Rerun == nil {
		return "[重跑不支持] 此表面不能重跑回合；新文本：\n" + text
	}
	if err := env.Rerun(text); err != nil {
		return "[重跑失败] " + err.Error()
	}
	return "[重跑] 已撤最后一轮并重发"
}

// mcpReadResource reads one resource and sends it as a message（spec
// 规则 31）：@server:uri → resources/read，文本以「资源 <uri>：」围
// 栏并入直接发送；非文本给可读报错。
func mcpReadResource(env *Env, target string) string {
	server, uri, found := strings.Cut(target, ":")
	if !found || server == "" || uri == "" {
		return "[用法] /mcp @<server>:<uri>（/mcp 查看全部）"
	}
	text, err := env.Svc.MCPReadResource(server, uri)
	if err != nil {
		return "[mcp 失败] " + err.Error() + "\n" + mcpList(env)
	}
	if env.Send == nil {
		return "[mcp 不支持] 此表面不能直接发送，资源内容：\n" + text
	}
	env.Send(wrapResource(uri, text))
	return "[mcp] 已发送资源 " + uri
}

// wrapResource 把资源文本并入可发送消息：「资源 <uri>：」+ 动态围栏
// （issue #178：Markdown 资源含行首裸 ``` 会破固定围栏——与导出/stdin/
// @ 引用同一 FenceFor 方案）。
func wrapResource(uri, text string) string {
	fence := export.FenceFor(text)
	return "资源 " + uri + "：\n" + fence + "\n" + text + "\n" + fence
}

// mcpAdd 配置一个 MCP 服务器（spec 规则 58）：/mcp add <名> -- <命令> [参数…]。
// `--` 后按空白切分（无引号语义——复杂命令进 settings.json）。
func mcpAdd(env *Env, args string) string {
	rest := args
	if i := strings.Index(rest, "--"); i >= 0 {
		rest = rest[i+2:]
	} else {
		return "[MCP] 用法：/mcp add <名> -- <命令> [参数…]"
	}
	fields := strings.Fields(args[:strings.Index(args, "--")])
	if len(fields) != 1 {
		return "[MCP] 用法：/mcp add <名> -- <命令> [参数…]（名一个、不含空格）"
	}
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return "[MCP] 用法：/mcp add <名> -- <命令> [参数…]（命令必填）"
	}
	cfg := mcp.Config{Command: parts[0]}
	if len(parts) > 1 {
		cfg.Args = parts[1:]
	}
	if err := env.Svc.SetMCPServer(fields[0], cfg); err != nil {
		return "[MCP 失败] " + err.Error()
	}
	summary := cfg.Command
	if len(cfg.Args) > 0 {
		summary += " " + strings.Join(cfg.Args, " ")
	}
	return "[MCP] 已配置 " + fields[0] + "：" + summary + "（热重载；/mcp 查看，握手失败见诊断）"
}

// mcpRemove 移除一个 MCP 服务器（spec 规则 58）。
func mcpRemove(env *Env, name string) string {
	if name == "" {
		return "[MCP] 用法：/mcp remove <名>"
	}
	if err := env.Svc.RemoveMCPServer(name); err != nil {
		return "[MCP 失败] " + err.Error()
	}
	return "[MCP] 已移除 " + name
}

// FormatAgents 渲染后台子代理清单（slash 与 TUI 面板同源，spec 规则
// 58）：每行 状态标记 + ID + 运行耗时或终态；maxRows 折叠余量。
func FormatAgents(views []tools.AgentJobView, maxRows int) string {
	if len(views) == 0 {
		return "[代理] 没有后台子代理（agent 工具启动）"
	}
	running := 0
	for _, v := range views {
		if v.State == tools.AgentRunning {
			running++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[代理] %d 个（运行 %d）：\n", len(views), running)
	for i, v := range views {
		if i == maxRows {
			fmt.Fprintf(&b, "  …+%d\n", len(views)-maxRows)
			break
		}
		mark := "?"
		switch v.State {
		case tools.AgentRunning:
			mark = "▶"
		case tools.AgentDone:
			mark = "✓"
		case tools.AgentFailed:
			mark = "✕"
		case tools.AgentStopped:
			mark = "■"
		}
		line := "  " + mark + " " + v.ID
		if v.State == tools.AgentRunning {
			line += " " + v.Duration.Round(time.Second).String()
		}
		if task := firstLineOfAgent(v.Prompt); task != "" {
			line += " " + task
		}
		if v.State == tools.AgentFailed && v.Err != "" {
			line += "（" + v.Err + "）"
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// firstLineOfAgent 取任务首行并截短（清单行宽）。
func firstLineOfAgent(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// hostOf 取端点的主机名（剖面回执展示；无 scheme 直接截断路径段）。
func hostOf(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	if i := strings.Index(baseURL, "://"); i >= 0 {
		baseURL = baseURL[i+3:]
	}
	if i := strings.IndexAny(baseURL, "/"); i >= 0 {
		baseURL = baseURL[:i]
	}
	return baseURL
}

// permsExport 把会话「总是允许」规则渲染成 .zgorules.json 建议
//（spec 规则 105）：会话规则随会话消散，固化成项目规则（审阅后落盘
// 工作区根）才能跨会话。输出带说明头 + 纯 JSON 体。
func permsExport(env *Env) string {
	text, err := env.Svc.SuggestProjectRules(env.SessionID())
	if err != nil {
		return "[权限] 固化失败：" + err.Error()
	}
	if text == "" {
		return "[权限] 本会话还没有「总是允许」规则可固化"
	}
	return "[权限] 审阅后把以下内容存为工作区根的 .zgorules.json（授信工作区生效）：\n" + text
}
