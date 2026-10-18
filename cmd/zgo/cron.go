package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/schedule"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runCron is the automation subcommand（specs/automation.md）：
//
//	zgo cron list
//	zgo cron add --schedule '*/5 * * * *' --prompt '...' --workspace path [--id name] [--model m]
//	zgo cron rm <id> | run <id> | enable <id> | disable <id>
//
// 管理（除 run）直接读写 jobs.json——桌面调度器每分钟热重载；run 走服务
// 的一次立即执行。
func runCron(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo cron <list|add|rm|run|enable|disable> …")
		return 2
	}
	cmd, rest := argv[0], argv[1:]
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	store := schedule.NewStore(dataDir)

	switch cmd {
	case "list":
		return cronList(store)
	case "add":
		return cronAdd(store, rest)
	case "rm":
		return cronMutation(store, rest, func(id string) error {
			ok, err := store.Remove(id)
			if err == nil && !ok {
				return fmt.Errorf("no job %s", id)
			}
			return err
		})
	case "enable":
		return cronMutation(store, rest, func(id string) error {
			return cronSetEnabled(store, id, true)
		})
	case "disable":
		return cronMutation(store, rest, func(id string) error {
			return cronSetEnabled(store, id, false)
		})
	case "run":
		return cronRun(dataDir, rest)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n", cmd)
		return 2
	}
}

func cronList(store *schedule.Store) int {
	jobs, err := store.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if len(jobs) == 0 {
		fmt.Println("（无定时任务）")
		return 0
	}
	jobs = schedule.SortedJobs(jobs)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t启用\t计划\t下次\t工作区\t模型\t上次状态")
	for _, j := range jobs {
		next := schedule.NextDue(j, time.Now())
		nextStr := "从不"
		if !next.IsZero() {
			nextStr = next.Format("01-02 15:04")
		}
		status := j.LastStatus
		if len(status) > 24 {
			status = status[:24] + "…"
		}
		model := j.Model
		if j.Window != "" {
			model += " ⏰" + j.Window
		}
		if model == "" {
			model = "（默认）"
		}
		fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Enabled, j.Schedule, nextStr, shortPath(j.Workspace), model, status)
	}
	w.Flush()
	return 0
}

func cronAdd(store *schedule.Store, args []string) int {
	fs := flag.NewFlagSet("cron add", flag.ContinueOnError)
	scheduleExpr := fs.String("schedule", "", "cron 表达式（分 时 日 月 周）")
	prompt := fs.String("prompt", "", "要运行的提示")
	workspace := fs.String("workspace", "", "会话工作区")
	model := fs.String("model", "", "覆盖模型（可选）")
	window := fs.String("window", "", "时间窗 HH:MM-HH:MM（可选，可跨午夜；空即不限）")
	id := fs.String("id", "", "任务 ID（缺省按提示生成）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scheduleExpr == "" || strings.TrimSpace(*prompt) == "" || *workspace == "" {
		fmt.Fprintln(os.Stderr, "需要 --schedule --prompt --workspace")
		return 2
	}
	if _, _, err := schedule.ParseWindow(*window); err != nil { // add 即校验（spec 规则 64）
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if _, err := schedule.ParseCron(*scheduleExpr); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	jobID := strings.TrimSpace(*id)
	if jobID == "" {
		jobID = fmt.Sprintf("job-%s", time.Now().Format("20060102-150405"))
	}
	abs, err := resolveWorkspaceArg(*workspace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	job := schedule.Job{ID: jobID, Schedule: *scheduleExpr, Prompt: strings.TrimSpace(*prompt),
		Workspace: abs, Model: *model, Window: *window, Enabled: true}
	if err := store.Upsert(job); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Printf("已添加 %s：%s @ %s\n", jobID, job.Schedule, abs)
	return 0
}

func cronMutation(store *schedule.Store, args []string, op func(string) error) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "需要任务 ID")
		return 2
	}
	if err := op(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Println("完成")
	return 0
}

func cronSetEnabled(store *schedule.Store, id string, on bool) error {
	ok, err := store.SetEnabled(id, on)
	if err == nil && !ok {
		return fmt.Errorf("no job %s", id)
	}
	return err
}

func cronRun(dataDir string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "需要任务 ID")
		return 2
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	defer svc.Close()
	svc.SetAsker(stdinAsker)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	summary, err := svc.RunJobNow(ctx, args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if summary != "" {
		fmt.Println(summary)
	}
	return 0
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func resolveWorkspaceArg(p string) (string, error) {
	if abs, err := filepath.Abs(p); err != nil {
		return "", err
	} else if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("工作区不存在：%s", abs)
	} else {
		return abs, nil
	}
}
