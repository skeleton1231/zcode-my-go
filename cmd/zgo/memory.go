package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runMemory manages workspace long-term memory（zgo memory 子命令，与
// cron/doctor/sessions 同族的运维面）：
//
//	zgo memory list                          各工作区记忆概况
//	zgo memory show --workspace <path>       查看某工作区记忆全文
//	zgo memory add --workspace <path> 文本   追加一条（带时间戳）
//	zgo memory clear --workspace <path>      清空（幂等）
//
// workspace 也接受会话 ID 的唯一前缀——解析到该会话的工作区。
func runMemory(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo memory <list|show|add|clear|search> [--workspace 路径] …")
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
		return memoryList(svc)
	case "show", "add", "clear":
		return memoryWithWorkspace(svc, cmd, rest)
	case "search":
		if len(rest) < 1 || rest[0] == "" {
			fmt.Fprintln(os.Stderr, "用法：zgo memory search <关键词>")
			return 2
		}
		return memorySearch(svc, rest[0])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n", cmd)
		return 2
	}
}

func memoryList(svc *service.AgentService) int {
	entries := svc.MemoryOverview()
	if len(entries) == 0 {
		fmt.Println("（无工作区记忆——先在某个工作区开会话或让 agent 记点什么）")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "工作区\t最近写入\t字节")
	for _, e := range entries {
		mod := e.Modified
		if mod == "" {
			mod = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\n", shortPath(e.Workspace), mod, e.Bytes)
	}
	w.Flush()
	return 0
}

func memoryWithWorkspace(svc *service.AgentService, cmd string, args []string) int {
	fs := flag.NewFlagSet("memory "+cmd, flag.ContinueOnError)
	wsFlag := fs.String("workspace", "", "工作区路径（或会话 ID 唯一前缀）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ws := resolveMemoryWorkspace(svc, *wsFlag)
	if ws == "" {
		fmt.Fprintln(os.Stderr, "需要 --workspace（路径，或 zgo sessions list 里会话的唯一前缀）")
		return 2
	}
	switch cmd {
	case "show":
		content := svc.ReadMemory(ws)
		if strings.TrimSpace(content) == "" {
			fmt.Println("（该工作区暂无记忆）")
			return 0
		}
		fmt.Print(content)
		return 0
	case "add":
		text := strings.TrimSpace(fs.Arg(0))
		if text == "" {
			fmt.Fprintln(os.Stderr, "add 需要一段文本（可用引号包裹）")
			return 2
		}
		if err := svc.AppendMemory(ws, text); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Println("已记录")
		return 0
	default: // clear
		if err := svc.ClearMemory(ws); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Println("已清空（幂等：没有记忆也算成功）")
		return 0
	}
}

// resolveMemoryWorkspace maps --workspace to a real path: a literal path
// wins; otherwise it is treated as a session id prefix.
func resolveMemoryWorkspace(svc *service.AgentService, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if _, err := os.Stat(v); err == nil {
		return v
	}
	if id, ok := svc.ResolveSession(v); ok {
		if ws := svc.SessionWorkspace(id); ws != "" {
			return ws
		}
	}
	return v // 路径形式但不存在：仍按原样（show 报无记忆，add 会建文件）
}

// memorySearch 跨工作区检索（spec 规则 119）：命中行按工作区分组打印。
func memorySearch(svc *service.AgentService, query string) int {
	hits := svc.SearchMemory(query)
	if len(hits) == 0 {
		fmt.Println("（没有命中——记忆是工作区级，zgo memory list 查看清单）")
		return 0
	}
	current := ""
	for _, h := range hits {
		if h.Workspace != current {
			current = h.Workspace
			fmt.Println(shortPath(h.Workspace) + ":")
		}
		fmt.Println("  " + h.Line)
	}
	return 0
}
