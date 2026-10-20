package main

// zgo skills（spec 规则 115）：技能层的终端查询面——list（名/描述/
// 层级/来源）与 show（SKILL.md 正文）。与 /skills 同一发现面（服务
// Skills），但面向人：不建会话、不注入提示，只读展示。信任门同款
//（未授信工作区不列其技能层——列表面不泄注入面）。

import (
	"fmt"
	"os"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runSkills(argv []string) int {
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
		return skillsList(svc, argv[1:])
	case "show":
		if len(argv) < 2 {
			fmt.Fprintln(os.Stderr, "用法：zgo skills show <名>")
			return 2
		}
		return skillsShow(svc, argv[1])
	default:
		fmt.Fprintln(os.Stderr, "用法：zgo skills <list|show> …")
		return 2
	}
}

func skillsList(svc *service.AgentService, rest []string) int {
	ws := ""
	if len(rest) > 0 {
		ws = rest[len(rest)-1] // 可选工作区路径（默认当前目录）
	} else if cwd, err := os.Getwd(); err == nil {
		ws = cwd
	}
	out := svc.Skills(ws)
	if len(out.Skills) == 0 && len(out.Diagnostics) == 0 {
		fmt.Println("（未发现技能——.zgo/skills 或 ~/.zgo/skills 放 SKILL.md）")
		return 0
	}
	for _, sk := range out.Skills {
		desc := sk.Description
		if desc == "" {
			desc = sk.WhenToUse
		}
		if desc == "" {
			desc = "（无描述）"
		}
		scope := "工作区"
		if sk.Scope == "user" {
			scope = "全局"
		} else if sk.Scope == "builtin" {
			scope = "内置"
		}
		fmt.Printf("%-24s %-4s %s\n", sk.Name, scope, desc)
	}
	for _, d := range out.Diagnostics {
		fmt.Fprintf(os.Stderr, "⚠ %s: %s\n", d.Path, d.Message)
	}
	return 0
}

func skillsShow(svc *service.AgentService, name string) int {
	cwd, _ := os.Getwd()
	body, ok := svc.SkillBody(cwd, name)
	if !ok {
		fmt.Fprintf(os.Stderr, "没有技能 %q（zgo skills list 查看；同名时工作区覆盖全局）\n", name)
		return 1
	}
	fmt.Println(strings.TrimRight(body, "\n"))
	return 0
}
