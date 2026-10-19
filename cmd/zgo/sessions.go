package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runSessions is the sessions subcommand（CLI 运维面，与 cron/doctor 同族）：
//
//	zgo sessions list                     表格：标题 · 时间 · 工作区 · token
//	zgo sessions export <id> [--md] [-o f] 导出 HTML（默认）或 Markdown
//	zgo sessions rm <id>                  删除
//	zgo sessions search <关键词>          跨会话检索（标题/消息/工具参数）
func runSessions(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo sessions <list|export|rm|search|sanitize|retired|restore> …")
		return 2
	}
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	defer svc.Close()

	cmd, rest := argv[0], argv[1:]
	switch cmd {
	case "list":
		return sessionsList(svc, rest)
	case "export":
		return sessionsExport(svc, rest)
	case "rm":
		return sessionsRm(svc, rest)
	case "archive":
		return sessionsArchive(svc, rest, true)
	case "unarchive":
		return sessionsArchive(svc, rest, false)
	case "retired":
		return sessionsRetired(svc)
	case "restore":
		return sessionsRestore(svc, rest)
	case "pin":
		return sessionsPin(svc, rest, true)
	case "unpin":
		return sessionsPin(svc, rest, false)
	case "search":
		return sessionsSearch(svc, rest)
	case "sanitize":
		return sessionsSanitize(svc, rest)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n", cmd)
		return 2
	}
}

func sessionsList(svc *service.AgentService, rest []string) int {
	// 归档过滤（spec 规则 82）：默认隐藏；--archived 只看归档；--all 全量。
	mode := "active"
	for _, a := range rest {
		if a == "--archived" || a == "--all" {
			mode = a[2:]
		}
	}
	var sessions []agent.SessionInfo
	for _, s := range svc.ListSessions() { // newest first
		switch mode {
		case "archived":
			if s.Archived {
				sessions = append(sessions, s)
			}
		case "all":
			sessions = append(sessions, s)
		default:
			if !s.Archived {
				sessions = append(sessions, s)
			}
		}
	}
	if len(sessions) == 0 {
		fmt.Println("（无会话）")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t标题\t更新\t工作区\ttokens(in/out)")
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "（未命名）"
		}
		if len([]rune(title)) > 28 {
			title = string([]rune(title)[:27]) + "…"
		}
		usage, _ := svc.SessionUsage(s.ID)
		if s.Pinned { // 置顶标记（spec 规则 86）
			title = "📌 " + title
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d/%d\n",
			shortID(s.ID), title, s.UpdatedAt.Local().Format("01-02 15:04"),
			shortPath(s.Workspace), usage.InputTokens, usage.OutputTokens)
	}
	w.Flush()
	return 0
}

func sessionsExport(svc *service.AgentService, args []string) int {
	// 旗标须在 id 之前（flag 包遇首个位置参数即停；-o 的取值不是旗标，
	// 简单重排会把它错位——保持标准语义，用法定格提示顺序）。
	fs := flag.NewFlagSet("sessions export", flag.ContinueOnError)
	asMarkdown := fs.Bool("md", false, "导出 Markdown（默认 HTML）")
	out := fs.String("o", "", "输出文件（默认 <id>.html/.md）")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法：zgo sessions export <id> [--md] [-o 文件]")
		return 2
	}
	id := fs.Arg(0)
	if full, ok := svc.ResolveSession(id); ok {
		id = full
	} else {
		fmt.Fprintf(os.Stderr, "没有会话 %s（完整 ID 见 zgo sessions list；支持唯一前缀）\n", id)
		return 1
	}
	dest := *out
	if dest == "" {
		ext := ".html"
		if *asMarkdown {
			ext = ".md"
		}
		dest = shortID(id) + ext
	}
	var err error
	if *asMarkdown {
		err = svc.ExportSessionMarkdown(id, dest)
	} else {
		err = svc.ExportSessionHTML(id, dest)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Println("已导出", dest)
	return 0
}

func sessionsRm(svc *service.AgentService, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "用法：zgo sessions rm <id>")
		return 2
	}
	full, ok := svc.ResolveSession(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "没有会话 %s\n", args[0])
		return 1
	}
	if err := svc.DeleteSession(full); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Println("已删除", shortID(args[0]))
	return 0
}

// sessionsSearch prints cross-session hits（桌面「搜索所有会话」的 CLI 面）
// ——首参 --regex 走正则形态（spec 规则 80：RE2、原大小写、(?i) 忽略）。
func sessionsSearch(svc *service.AgentService, args []string) int {
	useRegex := false
	if len(args) > 0 && args[0] == "--regex" {
		useRegex = true
		args = args[1:]
	}
	query := strings.Join(args, " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(os.Stderr, "用法：zgo sessions search [--regex] <关键词|正则>")
		return 2
	}
	var hits []service.SearchHit
	if useRegex {
		var err error
		hits, err = svc.SearchSessionsRegex(query)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
	} else {
		hits = svc.SearchSessions(query)
	}
	if len(hits) == 0 {
		fmt.Println("（没有匹配的会话）")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "会话	标题	片段")
	for _, h := range hits {
		title := h.Title
		if title == "" {
			title = "（未命名）"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", shortID(h.SessionID), title, h.Snippet)
	}
	w.Flush()
	fmt.Println()
	fmt.Println("zgo sessions export <前缀> 可导出命中的会话")
	return 0
}

// shortID keeps the table narrow: timestamps make IDs unique early.
func shortID(id string) string {
	if len(id) > 15 {
		return id[:15]
	}
	return id
}

// sessionsSanitize 清洗存量会话 JSONL 里的密钥（spec 规则 73 的收尾
// 半边：loop 咽喉点只保护新写入）。默认 dry-run 只报数；--apply 落盘
// （原子重写，未命中行字节不动）。密钥面与打码器同源：当前设置的
// 精确密钥（服务装载即喂）+ 高置信模式族。
func sessionsSanitize(svc *service.AgentService, rest []string) int {
	apply := false
	for _, a := range rest {
		switch a {
		case "--apply":
			apply = true
		default:
			fmt.Fprintf(os.Stderr, "未知参数 %q（--apply 落盘；缺省只报数）\n", a)
			return 2
		}
	}
	per, total, err := agent.SanitizeDir(svc.SessionDir(), !apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if len(per) == 0 {
		fmt.Println("（无会话可清洗）")
		return 0
	}
	for name, res := range per {
		if res.Redacted > 0 {
			fmt.Printf("%s：%d/%d 行%s\n", name, res.Redacted, res.Lines, applyNote(apply))
		}
	}
	if total == 0 {
		fmt.Println("未发现密钥形态（精确密钥与模式族双面）——无需清洗")
		return 0
	}
	if !apply {
		fmt.Printf("共 %d 行含密钥形态（dry-run，未落盘；加 --apply 清洗）\n", total)
	} else {
		fmt.Printf("已清洗 %d 行\n", total)
	}
	return 0
}

func applyNote(apply bool) string {
	if apply {
		return "（已清洗）"
	}
	return "（含密钥形态）"
}

// sessionsArchive 归档/取消归档（spec 规则 82）：接受 id 或唯一前缀；
// 归档会话从列表与 -c 恢复默认隐藏——内容不删（搜索/导出照常）。
func sessionsArchive(svc *service.AgentService, args []string, on bool) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "用法：zgo sessions %s <id|前缀>\n", map[bool]string{true: "archive", false: "unarchive"}[on])
		return 2
	}
	if err := svc.SetSessionArchived(args[0], on); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if on {
		fmt.Println("已归档（列表/-c 恢复默认隐藏；unarchive 取消；rm 删除）")
	} else {
		fmt.Println("已取消归档")
	}
	return 0
}

// sessionsPin 置顶/取消置顶（spec 规则 86）：置顶会话在列表最前（组
// 内仍按更新时间）——组织态与内容正交，不影响搜索/恢复/导出。
func sessionsPin(svc *service.AgentService, args []string, on bool) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "用法：zgo sessions %s <id|前缀>\n", map[bool]string{true: "pin", false: "unpin"}[on])
		return 2
	}
	if err := svc.SetSessionPinned(args[0], on); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if on {
		fmt.Println("已置顶（列表最前）")
	} else {
		fmt.Println("已取消置顶")
	}
	return 0
}

// sessionsRetired 列出保留期归档目录（spec 规则 98）——与组织态
// archive/unarchive 正交：这里是被保留期移出活跃面的文件。
func sessionsRetired(svc *service.AgentService) int {
	names := svc.ArchivedSessions()
	if len(names) == 0 {
		fmt.Println("（归档目录为空）")
		return 0
	}
	for _, n := range names {
		fmt.Println(n)
	}
	return 0
}

// sessionsRestore 把归档文件移回活跃面（spec 规则 98 的恢复面）。
func sessionsRestore(svc *service.AgentService, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "用法：zgo sessions restore <文件名|前缀>")
		return 2
	}
	name, ok := resolveRetiredName(svc, args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "归档目录没有 %q（zgo sessions retired 查看）\n", args[0])
		return 1
	}
	if err := svc.RestoreArchivedSession(name); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Printf("已恢复 %s 到活跃面（mtime 已刷新，不会被立即再归档）\n", name)
	return 0
}

// resolveRetiredName 归档文件名的唯一前缀解析。
func resolveRetiredName(svc *service.AgentService, prefix string) (string, bool) {
	var match string
	for _, n := range svc.ArchivedSessions() {
		if n == prefix || strings.HasPrefix(n, prefix) {
			if match != "" {
				return "", false
			}
			match = n
		}
	}
	return match, match != ""
}
