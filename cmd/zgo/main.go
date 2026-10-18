// The zgo command is the terminal agent (ZCode's zcode-cli print mode,
// translated): run a task in a workspace and stream the run to stdout.
//
//	zgo -p "解释这个仓库的结构"
//
// Without -p it prints usage; settings come from the environment or the
// desktop app's settings file, and permission asks are confirmed on stdin.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/export"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// usage 打印用法。任何解析失败、未知 flag、缺值都以 exit 2 结束
// （issue #35：此前四类误行为全部静默——缺值进 REPL、-p 吞 flag、
// 未知 flag 被跳过、-C 缺值吞掉）。
func usage(w io.Writer) {
	fmt.Fprintln(w, `用法：zgo [选项] [prompt]

  -p, --print <prompt>   运行一个任务后退出（与位置参数二选一）
  -t, --tui              全屏终端界面
  -C <dir>               工作区目录（默认当前目录）
  -c, --continue         恢复同工作区最近会话（无则新建）
  -r, --resume <前缀>    恢复指定会话（唯一前缀；-r 优先于 -c）
  --output-format <格式>  print 输出：text（默认）或 json（逐事件 JSONL）

不带 -p 时进入 REPL。`)
}

// parseArgs 严格解析 argv：取值型 flag 的值不得形似 flag（否则报错而非
// 静默吞掉），未知 flag 与缺值一律报错。
func parseArgs(argv []string) (prompt, workspace string, fullscreen bool, images []string, cont bool, resume, format string, err error) {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		next := ""
		if i+1 < len(argv) {
			next = argv[i+1]
		}
		switch {
		case arg == "-p" || arg == "--print":
			if next == "" || strings.HasPrefix(next, "-") {
				return "", "", false, nil, false, "", "", fmt.Errorf("-p/--print 需要一个 prompt 值")
			}
			i++
			prompt = argv[i]
		case arg == "-t" || arg == "--tui":
			fullscreen = true
		case arg == "-i" || arg == "--image":
			if next == "" || strings.HasPrefix(next, "-") {
				return "", "", false, nil, false, "", "", fmt.Errorf("-i/--image 需要一个图片路径")
			}
			i++
			images = append(images, argv[i])
		case arg == "-C":
			if next == "" || strings.HasPrefix(next, "-") {
				return "", "", false, nil, false, "", "", fmt.Errorf("-C 需要一个目录值")
			}
			i++
			workspace = argv[i]
		case arg == "--output-format":
			if next == "" || strings.HasPrefix(next, "-") {
				return "", "", false, nil, false, "", "", fmt.Errorf("--output-format 需要 text 或 json")
			}
			i++
			if next != "text" && next != "json" {
				return "", "", false, nil, false, "", "", fmt.Errorf("--output-format 只支持 text 或 json，得到 %q", next)
			}
			format = next
		case arg == "-c" || arg == "--continue":
			cont = true
		case arg == "-r" || arg == "--resume":
			if next == "" || strings.HasPrefix(next, "-") {
				return "", "", false, nil, false, "", "", fmt.Errorf("-r/--resume 需要一个会话前缀")
			}
			i++
			resume = argv[i]
		case strings.HasPrefix(arg, "-"):
			return "", "", false, nil, false, "", "", fmt.Errorf("未知参数 %q", arg)
		default:
			if prompt != "" {
				return "", "", false, nil, false, "", "", fmt.Errorf("多余的位置参数 %q（prompt 已由 %q 给出）", arg, prompt)
			}
			prompt = arg
		}
	}
	return prompt, workspace, fullscreen, images, cont, resume, format, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "cron" {
		os.Exit(runCron(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(runDoctor())
	}
	if len(os.Args) > 1 && os.Args[1] == "sessions" {
		os.Exit(runSessions(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "config" {
		os.Exit(runConfig(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "workflow" {
		os.Exit(runWorkflow(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "bugreport" {
		os.Exit(runBugreport(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		os.Exit(runEval(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "trust" {
		os.Exit(runTrust(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "usage" {
		os.Exit(runUsage(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "memory" {
		os.Exit(runMemory(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "models" {
		os.Exit(runModels(os.Args[2:]))
	}
	prompt, workspace, fullscreen, images, cont, resume, format, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "zgo: %v\n\n", err)
		usage(os.Stderr)
		os.Exit(2)
	}
	if fullscreen {
		if workspace == "" {
			if cwd, err := os.Getwd(); err == nil {
				workspace = cwd
			}
		}
		dataDir, err := userDataDir()
		if err != nil {
			fatal(err)
		}
		svc, err := service.New(dataDir)
		if err != nil {
			fatal(err)
		}
		defer svc.Close()
		tuiInfo, tuiResumed, tuiErr := resolveResume(svc, workspace, cont, resume)
		if tuiErr != nil {
			fatal(tuiErr)
		}
		os.Exit(runTUI(svc, workspace, tuiInfo, tuiResumed))
	}
	if prompt == "" {
		os.Exit(repl(workspace, cont, resume))
	}
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}

	dataDir, err := userDataDir()
	if err != nil {
		fatal(err)
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fatal(err)
	}
	svc.SetAsker(stdinAsker)
	svc.SetQuestion(stdinQuestion)

	info, resumed, rerr := resolveResume(svc, workspace, cont, resume)
	if rerr != nil {
		fatal(rerr)
	}
	if !resumed {
		info, err = svc.NewSession(workspace)
		if err != nil {
			fatal(err)
		}
		trustHint(os.Stderr, svc, workspace)
	}

	// 管道输入（spec 规则 27）：stdin 非终端（管道/重定向）时并入提示
	// 词——`git diff | zgo -p "review"` 一类自动化拼图的最后一块。
	if stdinIsPiped() {
		prompt = buildStdinPrompt(prompt, os.Stdin)
	}

	media, attachErrs := loadImageBlocks(images)
	for _, e := range attachErrs {
		fmt.Fprintln(os.Stderr, "附件：", e)
	}

	exit := 0
	lastStop := ""
	renderOut := render
	if format == "json" {
		renderOut = renderJSON // 规则 23：结构化输出供脚本消费
	}
	err = svc.ChatMedia(info.ID, prompt, media, func(ev agent.Event) {
		if ev.Kind == agent.EvAgentEnd {
			lastStop = string(ev.StopReason)
		}
		renderOut(ev)
	})
	if err == nil {
		if total, uerr := svc.SessionUsage(info.ID); uerr == nil &&
			(total.InputTokens != 0 || total.OutputTokens != 0) {
			fmt.Fprintf(os.Stderr, "[用量] 累计 ↑%d ↓%d tokens\n",
				total.InputTokens+total.CacheReadTokens+total.CacheWriteTokens, total.OutputTokens)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		exit = 1
	} else if lastStop == "error" {
		exit = 1 // 运行以错误告终（如缺少 API key），管道下游能感知
	}
	os.Exit(exit)
}

// render prints the run: text as it streams, tool activity as one line
// each, endings as notices.
func render(ev agent.Event) {
	switch ev.Kind {
	case agent.EvTextDelta:
		fmt.Print(ev.Text)
	case agent.EvMessageEnd:
		if ev.StopReason == "error" {
			fmt.Fprintf(os.Stderr, "\n[错误] %s\n", ev.ErrorMessage)
		} else if ev.StopReason != "end_turn" {
			fmt.Println()
		}
	case agent.EvToolExecutionStart:
		fmt.Fprintf(os.Stderr, "[工具] %s %s\n", ev.ToolName, oneLine(ev.Args))
	case agent.EvToolExecutionEnd:
		if ev.IsError {
			fmt.Fprintf(os.Stderr, "[工具] ✕ %s：%s\n", ev.ToolName, firstLine(ev.ResultContent))
		}
		if ev.Diff != "" {
			fmt.Fprint(os.Stderr, ev.Diff)
		}
	case agent.EvCompaction:
		fmt.Fprintf(os.Stderr, "[压缩] %s\n", ev.Text)
	case agent.EvBudgetNotice:
		fmt.Fprintf(os.Stderr, "[预算] %s\n", ev.Text)
	case agent.EvTurnRetry:
		// 重试必须可见（issue #40）：否则半截文本与新文本之间没有任何
		// 说明，用户毫无感知。
		fmt.Fprintf(os.Stderr, "\n[重试] %s\n", ev.Text)
	}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	// 按 rune 截断（issue #40）：按字节截断会把中文切开，非法 UTF-8
	// 直出终端。
	if runes := []rune(s); len(runes) > 60 {
		return string(runes[:59]) + "…"
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return oneLine(s)
}

// stdinAsker confirms permission asks on the terminal: y allows once,
// a always (this session), anything else denies.
func stdinAsker(req agent.AskRequest) agent.AskResponse {
	fmt.Fprintf(os.Stderr, "\n需要确认：%s %s（%s）\n", req.Tool, oneLine(req.Args), req.Reason)
	if req.Diff != "" {
		fmt.Fprint(os.Stderr, req.Diff)
	}
	fmt.Fprint(os.Stderr, "[y]允许一次 [a]本会话总是允许 [其他]拒绝 > ")
	line, err := readLine()
	if err != nil {
		return agent.AskResponse{Deny: true, Note: "终端读取失败"}
	}
	switch strings.TrimSpace(line) {
	case "y", "Y":
		return agent.AskResponse{}
	case "a", "A":
		return agent.AskResponse{Always: true}
	default:
		return agent.AskResponse{Deny: true, Note: "用户在终端选择了拒绝"}
	}
}

// stdinQuestion delivers the ask tool's question to the terminal.
func stdinQuestion(_ context.Context, q agent.Question) (string, error) {
	fmt.Fprintf(os.Stderr, "\nAgent 提问：%s\n", q.Text)
	for i, option := range q.Options {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, option)
	}
	fmt.Fprint(os.Stderr, "回答 > ")
	line, err := readLine()
	if err != nil {
		return "", err
	}
	answer := strings.TrimSpace(line)
	if n := 0; answer != "" {
		_, err := fmt.Sscanf(answer, "%d", &n)
		if err == nil && n >= 1 && n <= len(q.Options) {
			return q.Options[n-1], nil
		}
	}
	if answer == "" {
		return "", fmt.Errorf("用户没有回答")
	}
	return answer, nil
}

var stdin = bufio.NewReader(os.Stdin)

func readLine() (string, error) {
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// userDataDir mirrors the desktop app's settings location, so the CLI and
// the app share configuration.
func userDataDir() (string, error) {
	if dir := os.Getenv("ZGO_TEST_DATADIR"); dir != "" {
		return dir, nil // 测试隔离（cron_test）
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Zgo"), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误：", err)
	os.Exit(1)
}

// repl runs the interactive loop (zcode-cli's interactive mode): one
// session, streamed turns, slash commands. It returns the exit code.
// resumed（--continue/--resume 解析结果）时沿用该会话（历史随 Chat 全
// 量带上，无需回放——REPL 本就不重绘历史）.
func repl(workspace string, cont bool, resume string) int {
	var pending attachments // /attach 攒的图，随下一条消息发送
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}
	dataDir, err := userDataDir()
	if err != nil {
		fatal(err)
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fatal(err)
	}
	defer svc.Close()
	svc.SetAsker(stdinAsker)
	svc.SetQuestion(stdinQuestion)

	info, resumed, rerr := resolveResume(svc, workspace, cont, resume)
	if rerr != nil {
		fatal(rerr)
	}
	if !resumed {
		info, err = svc.NewSession(workspace)
		if err != nil {
			fatal(err)
		}
		trustHint(os.Stderr, svc, workspace)
	}
	if resumed {
		// 恢复会话沿用其自身工作区（即使与当前目录不同）。
		workspace = info.Workspace
		fmt.Fprintf(os.Stderr, "zgo 交互模式 · 恢复会话 %s · %s\n", shortID(info.ID), info.Title)
	} else {
		fmt.Fprintf(os.Stderr, "zgo 交互模式 · 工作区 %s\n", workspace)
	}
	fmt.Fprintln(os.Stderr, "命令：/exit 退出 · /compact 压缩 · /plan 切换计划模式 · /new 新会话")
	// 交互终端走行编辑器（raw 模式 + 历史，spec 规则 32）；管道/
	// 重定向退回 Scanner（行为不变）。
	var ed *editorState
	if e := newEditor(); e != nil {
		ed = e
		defer ed.Close()
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	readNext := func() (string, bool) {
		if ed != nil {
			line, _, quit := ed.readLine("zgo> ")
			if quit {
				return "", false
			}
			return line, true
		}
		fmt.Fprint(os.Stderr, "zgo> ")
		if !sc.Scan() {
			return "", false
		}
		return sc.Text(), true
	}
	for {
		text, alive := readNext()
		if !alive {
			if ed != nil {
				fmt.Fprintln(os.Stderr)
				return 0
			}
			if e := sc.Err(); e != nil {
				// 超过 1MB 的行曾与 EOF 同路径静默退出 REPL（issue #39）：
				// 丢弃该行并重建 scanner 继续读（ErrTooLong 后 scanner 不可
				// 恢复；重建后继续消费该行剩余部分直到换行）。
				fmt.Fprintln(os.Stderr, "\n[输入] 行超过 1MB，已丢弃；请分段粘贴")
				sc = bufio.NewScanner(os.Stdin)
				sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				continue
			}
			fmt.Fprintln(os.Stderr)
			return 0
		}
		line := strings.TrimSpace(text)
		handled, quit := runSlashCommand(&commandEnv{
			svc: svc, info: &info, workspace: workspace,
			out:    func(msg string) { fmt.Fprintln(os.Stderr, msg) },
			attach: &pending,
			send: func(text string) { // /mcp 展开：常规发送路径复用
				if err := svc.ChatMedia(info.ID, text, pending.take(), func(ev agent.Event) { render(ev) }); err != nil {
					fmt.Fprintln(os.Stderr, "错误：", err)
				}
				fmt.Println()
			},
			rerun: func(text string) error { // /retry、/resend：撤最后一轮重发
				fmt.Fprintln(os.Stderr, "[重跑] 撤最后一轮并重发：", text)
				return svc.EditAndResend(info.ID, text, func(ev agent.Event) { render(ev) })
			},
			find: func(q string) string { // /find：REPL 无视口——列命中清单（spec 规则 56）
				return replFind(svc, info.ID, q)
			},
		}, line)
		if quit {
			return 0
		}
		if handled {
			continue
		}
		// 运行中插话（spec 规则 26）：入队下一回合生效。
		if steered, notice := maybeSteer(svc, info.ID, line); steered {
			fmt.Fprintln(os.Stderr, notice)
			continue
		}
		if err := svc.ChatMedia(info.ID, line, pending.take(), func(ev agent.Event) { render(ev) }); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
		}
		fmt.Println()
	}
}

// resolveResume 把 -c/-r 解析成既有会话（规则 22）：--resume 走唯一前
// 缀（未知名报错，提示 zgo sessions list）；--continue 取同工作区最近
// 会话，无候选返回未恢复（调用方退回新建——首用不报错）；-r 优先于
// -c。返回 (info, resumed, err)。
func resolveResume(svc *service.AgentService, workspace string, cont bool, prefix string) (agent.SessionInfo, bool, error) {
	if prefix != "" {
		id, ok := svc.ResolveSession(prefix)
		if !ok {
			return agent.SessionInfo{}, false,
				fmt.Errorf("没有会话 %q（完整 ID 见 zgo sessions list；支持唯一前缀）", prefix)
		}
		for _, info := range svc.ListSessions() {
			if info.ID == id {
				return info, true, nil
			}
		}
	}
	if cont {
		if info, ok := svc.ContinueSession(workspace); ok {
			return info, true, nil
		}
	}
	return agent.SessionInfo{}, false, nil
}

// stdinCapRunes bounds piped stdin content（rune 计，与 @ 引用
// mentionTextCap 同口径：字符数封顶而非字节——ASCII 管道（git diff 类
// 是主场景）保满额度，rune 截断不裂字符）.
const stdinCapRunes = 60 << 10

// stdinIsPiped reports whether stdin is a pipe or redirect（非字符设
// 备）——交互终端与 TUI/REPL 场景返回 false.
func stdinIsPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// buildStdinPrompt 把管道输入并入提示词（spec 规则 27）：「标准输入：」
// 围栏附尾（与 @ 引用同款形态）；空输入原样返回；超限按 rune 截断到
// stdinCapRunes 字符并注明。读侧以 4×上限字节封顶（一个 rune 至多 4
// 字节——该字节数内必能容纳全部额度字符，也是防巨型管道撑爆内存的
// 硬界）.
func buildStdinPrompt(prompt string, r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, stdinCapRunes*4+1))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return prompt
	}
	content := string(data)
	note := ""
	if runes := []rune(content); len(runes) > stdinCapRunes {
		content = string(runes[:stdinCapRunes])
		note = "\n…（管道输入超长，已截断）"
	}
	// 动态围栏（issue #178，与 #71 同方案）：管道高频输入恰是 Markdown，
	// 行首裸 ``` 会提前闭合固定围栏——按内容最长反引号串 +1 加长。
	fence := export.FenceFor(content)
	return prompt + "\n\n标准输入：\n" + fence + "\n" +
		strings.TrimRight(content, "\n") + "\n" + fence + note
}

// replFind 在会话转录中搜索并列出命中（首 20 条 + 片段）。
func replFind(svc *service.AgentService, sessionID, q string) string {
	msgs, err := svc.Messages(sessionID)
	if err != nil {
		return "[搜索失败] " + err.Error()
	}
	lower := strings.ToLower(q)
	type hit struct {
		turn int
		text string
	}
	var hits []hit
	turn := 0
	for _, m := range msgs {
		if m.Role == ai.RoleUser {
			turn++
		}
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockText && strings.Contains(strings.ToLower(b.Text), lower) {
				hits = append(hits, hit{turn, b.Text})
			}
		}
		if strings.Contains(strings.ToLower(m.Text()), lower) && m.Text() != "" {
			hits = append(hits, hit{turn, m.Text()})
		}
	}
	if len(hits) == 0 {
		return "未找到 " + q
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "找到 %d 处（回合 · 片段）：", len(hits))
	for i, h := range hits {
		if i >= 20 {
			fmt.Fprintf(&sb, "\n… 还有 %d 处", len(hits)-20)
			break
		}
		snippet := strings.ReplaceAll(h.text, "\n", " ")
		if r := []rune(snippet); len(r) > 60 {
			snippet = string(r[:60]) + "…"
		}
		fmt.Fprintf(&sb, "\n  回合 %d：%s", h.turn, snippet)
	}
	return sb.String()
}
