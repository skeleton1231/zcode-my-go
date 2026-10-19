package main

// zgo mcp serve：把 zgo 工具面暴露成 MCP 服务端（stdio JSON-RPC，
// spec 规则 92）。默认只读四件（read/grep/glob/ls）；--all 显式开放
// 全量工具（含 write/edit/bash/patch——本地进程由用户亲自起，无确认
// 通道的风险自担）。工作区根 = 当前目录。

import (
	"context"
	"fmt"
	"os"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/mcpserver"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

func runMCPServe(argv []string) int {
	all := false
	for _, a := range argv {
		if a != "--all" {
			fmt.Fprintf(os.Stderr, "zgo mcp serve [--all]（未知参数 %q）\n", a)
			return 2
		}
		all = true
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	mcpserver.SetVersion(zgoVersion)
	srv := mcpserver.New(buildMCPTools(tools.Workspace{Root: wd}, all), os.Stdout)
	if err := srv.Serve(context.Background(), os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	return 0
}

// buildMCPTools 组装服务端工具面：默认只读（read/grep/glob/ls，
// spec 规则 92 的安全默认）；--all 走全量内置工具面。
func buildMCPTools(w tools.Workspace, all bool) []agent.Tool {
	if all {
		return tools.AllW(w, tools.NewStates(), nil)
	}
	return tools.ReadOnlyW(w)
}
