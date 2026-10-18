package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/workflow"
)

// runWorkflow 是工作流的运维子命令（spec 规则 48，只读视图——执行与
// 续跑是 agent 动作，经 workflow 工具走会话权限面）：
//
//	zgo workflow list           已保存定义（名/步骤数/保存时间）
//	zgo workflow show <名>      定义详情（步骤表含 after 依赖）
//	zgo workflow runs [N]       最近 N 条运行记录（默认 20；含状态与进度）
func runWorkflow(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo workflow <list|show|runs> …")
		return 2
	}
	cmd, rest := argv[0], argv[1:]
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
	store := svc.WorkflowStore()

	switch cmd {
	case "list":
		defs := store.List()
		if len(defs) == 0 {
			fmt.Println("还没有已保存的工作流（会话里 workflow 工具带 saveAs 保存）")
			return 0
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t步骤\t保存时间")
		for _, d := range defs {
			fmt.Fprintf(w, "%s\t%d\t%s\n", d.Name, len(d.Steps), d.SavedAt.Format("2006-01-02 15:04"))
		}
		w.Flush()
		return 0
	case "show":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "用法：zgo workflow show <名>")
			return 2
		}
		def, err := store.Get(rest[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		if def.Description != "" {
			fmt.Println(def.Description)
		}
		for i, s := range def.Steps {
			line := fmt.Sprintf("%2d. [%s] %s", i+1, s.Kind, s.Description)
			if s.Name != "" {
				line = fmt.Sprintf("%2d. [%s] %s（%s）", i+1, s.Kind, s.Description, s.Name)
			}
			fmt.Println(line)
			if len(s.After) > 0 {
				fmt.Printf("      after: %s\n", strings.Join(s.After, ", "))
			}
		}
		return 0
	case "runs":
		limit := 20
		if len(rest) == 1 {
			var n int
			if _, err := fmt.Sscanf(rest[0], "%d", &n); err == nil && n > 0 {
				limit = n
			}
		}
		runs := store.ListRuns(limit)
		if len(runs) == 0 {
			fmt.Println("还没有运行记录")
			return 0
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "运行 ID\t工作流\t状态\t进度\t开始时间")
		for _, r := range runs {
			name := r.Name
			if name == "" {
				name = "（临场）"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%s\n", r.ID, name, r.Status,
				countDone(r.Steps), len(r.Steps), r.StartedAt.Format("01-02 15:04:05"))
		}
		w.Flush()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q（list | show <名> | runs [N]）\n", cmd)
		return 2
	}
}

// countDone 统计已完成步骤数。
func countDone(steps []workflow.RunStep) int {
	n := 0
	for _, s := range steps {
		if s.Done {
			n++
		}
	}
	return n
}
