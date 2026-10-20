package main

// zgo agents（spec 规则 116）：子代理定义层的终端查询面——list（名/
// 描述/层级/工具白名单）与 show（定义文件原文：frontmatter + persona）。
// 与系统提示注入/task 路由同一发现面（服务 agentRoots）；信任门同款
//（未授信工作区不列其层）。

import (
	"fmt"
	"os"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runAgents(argv []string) int {
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

	cmd := "list"
	if len(argv) > 0 {
		cmd = argv[0]
	}
	switch cmd {
	case "list":
		return agentsList(svc, argv[1:])
	case "show":
		if len(argv) < 2 {
			fmt.Fprintln(os.Stderr, "用法：zgo agents show <名> [工作区]")
			return 2
		}
		ws := ""
		if len(argv) > 2 {
			ws = argv[2]
		} else if cwd, err := os.Getwd(); err == nil {
			ws = cwd
		}
		return agentsShow(svc, ws, argv[1])
	default:
		fmt.Fprintln(os.Stderr, "用法：zgo agents <list|show> …")
		return 2
	}
}

func agentsList(svc *service.AgentService, rest []string) int {
	ws := ""
	if len(rest) > 0 {
		ws = rest[len(rest)-1]
	} else if cwd, err := os.Getwd(); err == nil {
		ws = cwd
	}
	out := svc.Agents(ws)
	if len(out.Defs) == 0 && len(out.Diagnostics) == 0 {
		fmt.Println("（未发现子代理定义——.zgo/agents 或 ~/.zgo/agents 放 <名>.md）")
		return 0
	}
	for _, d := range out.Defs {
		desc := d.Description
		if desc == "" {
			desc = "（无描述）"
		}
		tools := "只读族"
		if len(d.Tools) > 0 {
			tools = strings.Join(d.Tools, ",")
		}
		scope := "工作区"
		if d.Scope == "user" {
			scope = "全局"
		}
		fmt.Printf("%-20s %-4s %-24s %s\n", d.Name, scope, tools, desc)
	}
	for _, dg := range out.Diagnostics {
		fmt.Fprintf(os.Stderr, "⚠ %s: %s\n", dg.Path, dg.Message)
	}
	return 0
}

func agentsShow(svc *service.AgentService, ws, name string) int {
	body, ok := svc.AgentBody(ws, name)
	if !ok {
		fmt.Fprintf(os.Stderr, "没有子代理 %q（zgo agents list 查看）\n", name)
		return 1
	}
	fmt.Println(strings.TrimRight(body, "\n"))
	return 0
}
