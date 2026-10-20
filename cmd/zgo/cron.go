package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
//	zgo cron add --at '每天下午3点' --prompt '...' --workspace path   （自然语言，spec 规则 95）
//	zgo cron rm <id> | run <id> | enable <id> | disable <id>
//	zgo cron serve [--once]                                     （常驻/单轮调度，spec 规则 123）
//
// 管理（除 run）直接读写 jobs.json——桌面调度器每分钟热重载；run 走服务
// 的一次立即执行。
func runCron(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo cron <list|add|rm|run|enable|disable|serve> …")
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
	case "next":
		return cronNext(store, rest)
	case "log":
		return cronLog(store, rest)
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
	case "serve":
		return cronServe(dataDir, rest)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n", cmd)
		return 2
	}
}

// cronServe 是调度的常驻/单轮形态（spec 规则 123）：默认前台常驻（每分
// 钟 tick，Ctrl+C 停止）；--once 跑完当前到期任务即退——系统定时器
//（Windows 任务计划/systemd timer）周期拉起 --once 即得无人值守调度，
// 不必守进程。运行历史照常落 job-logs（zgo cron log 查看）。
func cronServe(dataDir string, argv []string) int {
	once := false
	for _, a := range argv {
		if a == "--once" {
			once = true
			continue
		}
		fmt.Fprintln(os.Stderr, "用法：zgo cron serve [--once]")
		return 2
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	defer svc.Close()
	if once {
		svc.RunDueOnce(context.Background())
		return 0
	}
	svc.StartScheduler()
	fmt.Println("调度已启动（每分钟检查到期任务；Ctrl+C 停止）")
	// 信号触发优雅退出：先停调度（等在飞任务收尾），Close 收束其余。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	svc.StopScheduler()
	fmt.Println("调度已停止")
	return 0
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
		plan := j.Schedule
		if j.At != "" { // 自然语言原文回显（spec 规则 95）
			plan += "（" + j.At + "）"
		}
		fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Enabled, plan, nextStr, shortPath(j.Workspace), model, status)
	}
	w.Flush()
	return 0
}

func cronAdd(store *schedule.Store, args []string) int {
	fs := flag.NewFlagSet("cron add", flag.ContinueOnError)
	scheduleExpr := fs.String("schedule", "", "cron 表达式（分 时 日 月 周）")
	at := fs.String("at", "", "自然语言调度（如 每天下午3点；与 --schedule 互斥）")
	prompt := fs.String("prompt", "", "要运行的提示")
	workspace := fs.String("workspace", "", "会话工作区")
	model := fs.String("model", "", "覆盖模型（可选）")
	window := fs.String("window", "", "时间窗 HH:MM-HH:MM（可选，可跨午夜；空即不限）")
	id := fs.String("id", "", "任务 ID（缺省按提示生成）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scheduleExpr != "" && *at != "" {
		fmt.Fprintln(os.Stderr, "--at 与 --schedule 互斥——二选一")
		return 2
	}
	if *scheduleExpr == "" && *at == "" {
		fmt.Fprintln(os.Stderr, "需要 --schedule（或 --at）--prompt --workspace")
		return 2
	}
	if strings.TrimSpace(*prompt) == "" || *workspace == "" {
		fmt.Fprintln(os.Stderr, "需要 --schedule（或 --at）--prompt --workspace")
		return 2
	}
	// 自然语言先翻成五字段再走同一校验/落库路径（spec 规则 95）。
	atOrigin := ""
	if *at != "" {
		expr, err := schedule.ParseNatural(*at)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		*scheduleExpr = expr
		atOrigin = strings.TrimSpace(*at)
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
	job := schedule.Job{ID: jobID, Schedule: *scheduleExpr, At: atOrigin,
		Prompt:    strings.TrimSpace(*prompt),
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

// cronNext 推演任务未来触发时刻（spec 规则 108）：zgo cron next <id>
// [--count N]（默认 3；含时间窗顺延语义——窗口外到期的补跑点也如实
// 呈现）。
func cronNext(store *schedule.Store, args []string) int {
	if len(args) < 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "用法：zgo cron next <id> [--count N]")
		return 2
	}
	id := args[0]
	count := 3
	for _, a := range args[1:] {
		if n, err := strconv.Atoi(strings.TrimPrefix(a, "--count=")); err == nil && strings.HasPrefix(a, "--count=") && n > 0 && n <= 50 {
			count = n
		}
	}
	jobs, err := store.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	var job *schedule.Job
	for i := range jobs {
		if jobs[i].ID == id {
			job = &jobs[i]
			break
		}
	}
	if job == nil {
		fmt.Fprintf(os.Stderr, "没有任务 %q（zgo cron list 查看）\n", id)
		return 1
	}
	times := schedule.PreviewNext(*job, time.Now(), count)
	if len(times) == 0 {
		fmt.Printf("任务 %s 无未来触发（disabled 或计划式坏）\n", id)
		return 0
	}
	for i, t := range times {
		rel := time.Until(t).Round(time.Minute)
		fmt.Printf("%d. %s（%s 后）\n", i+1, t.Format("2006-01-02 15:04 Mon"), rel)
	}
	return 0
}

// cronLog 查任务运行历史（spec 规则 118）：zgo cron log <id> [--limit N]
// （默认 20，新到旧）。
func cronLog(store *schedule.Store, args []string) int {
	if len(args) < 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "用法：zgo cron log <id> [--limit N]")
		return 2
	}
	id := args[0]
	limit := 20
	for _, a := range args[1:] {
		if n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit=")); err == nil && strings.HasPrefix(a, "--limit=") && n > 0 && n <= 200 {
			limit = n
		}
	}
	jobs, err := store.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	known := false
	for _, j := range jobs {
		if j.ID == id {
			known = true
		}
	}
	if !known {
		fmt.Fprintf(os.Stderr, "没有任务 %q（zgo cron list 查看）\n", id)
		return 1
	}
	entries := schedule.RunHistory(filepath.Dir(store.Path()), id, limit)
	if len(entries) == 0 {
		fmt.Printf("任务 %s 还没有运行记录\n", id)
		return 0
	}
	for _, e := range entries {
		fmt.Printf("%s  %-6s %s\n", e.Time.Format("01-02 15:04"), statusMark(e.Status), oneLineLog(e.Summary, 80))
	}
	return 0
}

// statusMark 状态短标（ok/err/skip）。
func statusMark(status string) string {
	switch {
	case status == "ok":
		return "ok"
	case strings.HasPrefix(status, "error"):
		return "ERR"
	case strings.HasPrefix(status, "skipped"):
		return "skip"
	}
	return status
}

// oneLineLog 压成单行摘要（换行转空格 + 截断）——main.go 已有
// oneLine（单参形态），此处独立命名避免撞名。
func oneLineLog(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
