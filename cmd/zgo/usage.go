package main

// zgo usage：会话 token 用量的 CLI 面（spec 规则 25）——无参取最近会
// 话，唯一前缀指定；与 /usage 同一张表（service.FormatUsage）。

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/pricing"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runUsage(argv []string) int {
	// --days N：近 N 日趋势（spec 规则 90）——只读历史账，不需要会话。
	days := 0
	var rest []string
	for _, a := range argv {
		if strings.HasPrefix(a, "--days") && a != "--days" {
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--days=")); err == nil {
				days = n
				continue
			}
		}
		rest = append(rest, a)
	}
	argv = rest
	if days > 0 {
		return usageHistory(days)
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

	sessions := svc.ListSessions()
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "没有会话（zgo sessions list 查看）")
		return 1
	}
	target := sessions[0] // ListSessions 新会话在前：无参 = 最近
	if len(argv) > 0 {
		id, ok := svc.ResolveSession(argv[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "没有会话 %q（完整 ID 见 zgo sessions list；支持唯一前缀）\n", argv[0])
			return 1
		}
		for _, info := range sessions {
			if info.ID == id {
				target = info
				break
			}
		}
	}
	summary, err := svc.UsageSummaryOf(target.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Println(service.FormatUsage(summary))

	// 今日归因（spec 规则 89+93）：哪个会话/来源烧的预算（token 与 $）。
	today, budget := svc.DailyUsage()
	if today > 0 {
		sess, main, sub, wf := svc.UsageAttribution()
		cost := ""
		if c := svc.UsageCostToday(); c > 0 {
			cost = fmt.Sprintf("，≈ %s", pricing.FormatUSD(c))
		}
		fmt.Printf("今日累计 %d token（预算 %d）%s——来源：主循环 %d / 子代理 %d / 工作流 %d\n",
			today, budget, cost, main, sub, wf)
		fmt.Println("按会话（Top 5）：")
		for i, st := range sess {
			if i >= 5 {
				break
			}
			name := st.SessionID
			if name == "" {
				name = "（未归因）"
			} else if short := shortIDSafe(svc, name); short != "" {
				name = short
			}
			fmt.Printf("  %-16s %d\n", name, st.Tokens)
		}
	}
	return 0
}

// shortIDSafe 把会话 id 缩到列表展示形态（解析不到就原样）。
func shortIDSafe(svc *service.AgentService, id string) string {
	sessions := svc.ListSessions()
	for _, info := range sessions {
		if info.ID == id {
			if t := info.Title; t != "" {
				if len([]rune(t)) > 16 {
					t = string([]rune(t)[:15]) + "…"
				}
				return t
			}
			break
		}
	}
	return id
}

// usageHistory 打印近 N 日用量趋势（spec 规则 90+93）：每日总量 + 迷你
// 条形（▮ 按最大日比例）+ $ 成本列（按各条目实际模型牌价；无牌价条
// 目贡献 0）。缺日照列 0（趋势连续性）。
func usageHistory(days int) int {
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
	hist := svc.UsageHistory(days)
	max := 0
	for _, d := range hist {
		if d.Tokens > max {
			max = d.Tokens
		}
	}
	fmt.Printf("近 %d 日用量（计价 token）：%s\n", days, dataDir)
	for _, d := range hist {
		bar := ""
		if max > 0 {
			n := d.Tokens * 20 / max
			bar = strings.Repeat("▮", n) + strings.Repeat("▯", 20-n)
		} else {
			bar = strings.Repeat("▯", 20)
		}
		fmt.Printf("  %s  %s %6d  %8s\n", d.Day, bar, d.Tokens,
			pricing.FormatUSD(d.CostMicro))
	}
	return 0
}
