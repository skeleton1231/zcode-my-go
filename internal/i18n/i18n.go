// Package i18n 是语言与文案目录（spec 规则 94，ZCode packages/i18n
// 的 Go 化）：受控的 copy 面（CLI 帮助/预算文案/TUI 状态语），不是
// 全量字符串抽取——目录与代码同构（struct 而非 map），缺译编译期即
// 挡住。zh 是基准语言（存量文案原样迁入）；en 覆盖同一面。检测序：
// ZGO_LANG → LC_ALL → LC_MESSAGES → LANG（形如 zh_CN.UTF-8 归一到
// zh）。
package i18n

import (
	"os"
	"strings"
)

// Locale 是受支持的语言。
type Locale string

// 受支持语言（zh 为默认与回退——存量文案即简体中文）。
const (
	Zh Locale = "zh"
	En Locale = "en"
)

// Copy 是一个语言的全部文案。字段即契约：加键必须同时填两种语言
// （struct 同构保证——没有 map 时代的漏译检查）。
type Copy struct {
	Lang Locale
	CLI  struct {
		Help           string // zgo 无参帮助块
		NeedPrompt     string // -p 缺值
		UnknownFlag    string // 未知 flag（%s=flag）
		UnknownKey     string // config 未知键（%s=key）
		BadLangValue   string // lang 非法值（%s=值）
		NoSessions     string // 无会话
		UsageDaysBrief string // --days 头行（%d=天数 %s=数据目录）
	}
	TUI struct {
		Idle      string // 状态栏：就绪
		Running   string // 状态栏：运行中
		Stopping  string // 状态栏：中止中
		Rerunning string // 状态栏：重跑中
		Aborted   string // 黄显：Esc 中止
	}
	Budget struct {
		Near string // 80% 通知（%d=今日 token）
		Over string // 100% 通知（%d=今日 %d=预算）
		Fuse string // 硬预算熔断错误（%d=今日 %d=预算）
	}
	Session struct {
		Empty      string // 空消息错误
		InProgress string // 已有运行中回合
	}
	Compact struct {
		Failed string // 自动压缩失败通知（%d=第几次）
		Done   string // 压缩完成通知
	}
	Mentions struct {
		Skipped string // 超预算引用未展开尾注（%d=条数）
	}
}

// zh 返回基准目录（存量文案原样迁入，zh 读者零变化）。
func zh() Copy {
	var c Copy
	c.Lang = Zh
	c.CLI.Help = `用法：zgo [选项] [prompt]

  -p, --print <prompt>   运行一个任务后退出（与位置参数二选一）
  -t, --tui              全屏终端界面
  -C <dir>               工作区目录（默认当前目录）
  -c, --continue         恢复同工作区最近会话（无则新建）
  -r, --resume <前缀>    恢复指定会话（唯一前缀；-r 优先于 -c）
  --output-format <格式>  print 输出：text（默认）或 json（逐事件 JSONL）

不带 -p 时进入 REPL。`
	c.CLI.NeedPrompt = "-p/--print 需要一个 prompt 值"
	c.CLI.UnknownFlag = "未知参数 %s"
	c.CLI.UnknownKey = "未知键 %q"
	c.CLI.BadLangValue = "lang 只支持 auto/zh/en（收到 %q）"
	c.CLI.NoSessions = "没有会话（zgo sessions list 查看）"
	c.CLI.UsageDaysBrief = "近 %d 日用量（计价 token）：%s"
	c.TUI.Idle = "就绪"
	c.TUI.Running = "运行中…"
	c.TUI.Stopping = "中止中…"
	c.TUI.Rerunning = "重跑中…"
	c.TUI.Aborted = "[已中止] Esc 中断了当前回合"
	c.Budget.Near = "今日已用 %d token（预算的 80%%+）"
	c.Budget.Over = "今日已用 %d token，超出预算 %d"
	c.Budget.Fuse = "今日已计价 %d token，已达硬预算 %d——新回合被熔断（zgo config set hardBudgetStop false 可解除）"
	c.Session.Empty = "empty message"
	c.Session.InProgress = "a turn is already in progress"
	c.Compact.Failed = "自动压缩失败（第 %d 次）：%v"
	c.Compact.Done = "会话已压缩：旧消息汇总为摘要，保留最近对话"
	c.Mentions.Skipped = "（另有 %d 个引用因总体积/数量上限未展开，可分条追问）"
	return c
}

// en 覆盖同一面。
func en() Copy {
	c := zh() // 结构兜底：漏改的字段回落中文，好过空白
	c.Lang = En
	c.CLI.Help = `Usage: zgo [options] [prompt]

  -p, --print <prompt>   run one task and exit (mutually exclusive with positional)
  -t, --tui              fullscreen terminal UI
  -C <dir>               workspace directory (default: cwd)
  -c, --continue         resume the latest session in this workspace
  -r, --resume <prefix>  resume a session by unique prefix (-r wins over -c)
  --output-format <fmt>  print output: text (default) or json (per-event JSONL)

Without -p zgo enters the REPL.`
	c.CLI.NeedPrompt = "-p/--print needs a prompt value"
	c.CLI.UnknownFlag = "unknown argument %s"
	c.CLI.UnknownKey = "unknown key %q"
	c.CLI.BadLangValue = "lang supports auto/zh/en only (got %q)"
	c.CLI.NoSessions = "no sessions yet (see zgo sessions list)"
	c.CLI.UsageDaysBrief = "Last %d days of usage (priced tokens): %s"
	c.TUI.Idle = "ready"
	c.TUI.Running = "running…"
	c.TUI.Stopping = "stopping…"
	c.TUI.Rerunning = "rerunning…"
	c.TUI.Aborted = "[aborted] Esc interrupted the current turn"
	c.Budget.Near = "Used %d tokens today (80%%+ of budget)"
	c.Budget.Over = "Used %d tokens today, over the %d budget"
	c.Budget.Fuse = "Priced %d tokens today, hit the hard budget of %d — new turns are fused off (zgo config set hardBudgetStop false to release)"
	c.Session.Empty = "empty message"
	c.Session.InProgress = "a turn is already in progress"
	c.Compact.Failed = "auto-compaction failed (attempt %d): %v"
	c.Compact.Done = "Session compacted: older messages summarized, recent turns kept"
	c.Mentions.Skipped = "(%d more mentions skipped due to the total size/count cap; ask again in smaller batches)"
	return c
}

// Catalog 返回某语言的目录；未知语言回退 zh。
func Catalog(l Locale) Copy {
	if l == En {
		return en()
	}
	return zh()
}

// DetectEnv 从环境检测语言：ZGO_LANG → LC_ALL → LC_MESSAGES → LANG，
// 形如 zh_CN.UTF-8 / en_US 归一到 zh/en；未识别返回 ""（调用方决定
// 默认）。
func DetectEnv() Locale {
	for _, key := range []string{"ZGO_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			if l := normalize(v); l != "" {
				return l
			}
		}
	}
	return ""
}

// normalize 归一一个 locale 串（zh-CN.UTF-8@pinyin → zh）。
func normalize(v string) Locale {
	v = strings.ToLower(strings.TrimSpace(v))
	if i := strings.IndexAny(v, "._-@"); i >= 0 {
		v = v[:i]
	}
	switch v {
	case "zh":
		return Zh
	case "en":
		return En
	}
	return ""
}

// Resolve 把设置值解析成语言：auto/空 → 环境检测（未检出 → zh 默
// 认）；zh/en 直取；其他值也回退 zh（config set 面已挡非法值，这里
// 只兜底不报错——语言问题不该让命令崩）。
func Resolve(requested string) Locale {
	requested = strings.ToLower(strings.TrimSpace(requested))
	switch requested {
	case "zh":
		return Zh
	case "en":
		return En
	}
	if l := DetectEnv(); l != "" {
		return l
	}
	return Zh
}
