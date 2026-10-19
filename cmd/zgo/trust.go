package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runTrust 是工作区授信子命令（spec 规则 54+101）：
//
//	zgo trust preview <路径>  授信前预览注入面（规则/工具/命令/技能/
//	                          子代理——克隆来的仓库先看再决定）
//	zgo trust <路径>          授信（print/CI 预授信——未授信工作区的
//	                          .zgorules.json 与工作区命令/技能层不加载）
//	zgo trust --list          列出授信表
//	zgo trust --revoke <路径> 撤销授信
//
// printTrustPreview 渲染注入面清单（spec 规则 101）。
func printTrustPreview(svc *service.AgentService, abs string) {
	rep, err := svc.TrustPreview(abs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
	state := "未授信（以下内容当前不生效）"
	if rep.Trusted {
		state = "已授信（以下内容当前生效）"
	}
	fmt.Printf("%s — %s\n", rep.Workspace, state)
	if rep.Empty() {
		fmt.Println("注入面为空（无规则文件、无自定义层）——授信只影响后续添加的内容")
		return
	}
	if len(rep.AllowRules) > 0 {
		fmt.Println("权限规则（.zgorules.json 的 allow 是提权面）：")
		keys := make([]string, 0, len(rep.AllowRules))
		for k := range rep.AllowRules {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mark := ""
			if k == "*" {
				mark = "  ← 全工具放行（最大提权面）"
			}
			fmt.Printf("  allow %-12s ×%d%s\n", k, rep.AllowRules[k], mark)
		}
	}
	if rep.AskRules > 0 || rep.DenyRules > 0 || rep.RulesBroken {
		fmt.Printf("  ask ×%d / deny ×%d / 坏文件 %v\n", rep.AskRules, rep.DenyRules, rep.RulesBroken)
	}
	for _, sec := range []struct {
		title string
		names []string
	}{
		{"自定义工具（.zgo/tools，执行走 bash 通道）", rep.Tools},
		{"自定义命令（.zgo/commands）", rep.Commands},
		{"技能（.zgo/skills，工作区层）", rep.Skills},
		{"子代理定义（.zgo/agents）", rep.Agents},
	} {
		if len(sec.names) > 0 {
			fmt.Printf("%s：%s\n", sec.title, strings.Join(sec.names, ", "))
		}
	}
}

func runTrust(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo trust <路径> | --list | --revoke <路径>")
		return 2
	}
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if argv[0] == "preview" {
		if len(argv) < 2 {
			fmt.Fprintln(os.Stderr, "用法：zgo trust preview <路径>")
			return 2
		}
		abs, err := filepath.Abs(argv[1])
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
		printTrustPreview(svc, abs)
		return 0
	}
	if argv[0] == "--list" {
		svc, err := service.New(dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		defer svc.Close()
		entries := svc.TrustList()
		if len(entries) == 0 {
			fmt.Println("授信表为空")
			return 0
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "路径\t授信时间\t途径")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\n", e.Path, e.GrantedAt.Format("2006-01-02 15:04"), e.Via)
		}
		w.Flush()
		return 0
	}
	revoke := argv[0] == "--revoke"
	if revoke && len(argv) < 2 {
		fmt.Fprintln(os.Stderr, "用法：zgo trust --revoke <路径>")
		return 2
	}
	path := argv[len(argv)-1]
	abs, err := filepath.Abs(path)
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
	if revoke {
		if err := svc.RevokeWorkspace(abs); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Println("已撤销授信（在跑会话的项目规则即时清空）：", abs)
		return 0
	}
	if err := svc.TrustWorkspace(abs, "cli"); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Println("已授信（加载其 .zgorules.json 与命令/技能层）：", abs)
	return 0
}
