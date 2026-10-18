package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runTrust 是工作区授信子命令（spec 规则 53）：
//
//	zgo trust <路径>          授信（print/CI 预授信——未授信工作区的
//	                          .zgorules.json 与工作区命令/技能层不加载）
//	zgo trust --list          列出授信表
//	zgo trust --revoke <路径> 撤销授信
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
