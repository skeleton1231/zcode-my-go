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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func main() {
	prompt := ""
	workspace := ""
	fullscreen := false
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case arg == "-p" || arg == "--print":
			if i+1 < len(os.Args) {
				i++
				prompt = os.Args[i]
			}
		case arg == "-t" || arg == "--tui":
			fullscreen = true
		case arg == "-C" && i+1 < len(os.Args):
			i++
			workspace = os.Args[i]
		default:
			if !strings.HasPrefix(arg, "-") && prompt == "" {
				prompt = arg
			}
		}
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
		os.Exit(runTUI(svc, workspace))
	}
	if prompt == "" {
		os.Exit(repl(workspace))
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

	info, err := svc.NewSession(workspace)
	if err != nil {
		fatal(err)
	}

	exit := 0
	lastStop := ""
	err = svc.Chat(info.ID, prompt, func(ev agent.Event) {
		if ev.Kind == agent.EvAgentEnd {
			lastStop = string(ev.StopReason)
		}
		render(ev)
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
	case agent.EvCompaction:
		fmt.Fprintf(os.Stderr, "[压缩] %s\n", ev.Text)
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
	fmt.Fprintf(os.Stderr, "\n需要确认：%s %s（%s）\n[y]允许一次 [a]本会话总是允许 [其他]拒绝 > ",
		req.Tool, oneLine(req.Args), req.Reason)
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
func repl(workspace string) int {
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

	info, err := svc.NewSession(workspace)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "zgo 交互模式 · 工作区 %s\n命令：/exit 退出 · /compact 压缩 · /plan 切换计划模式 · /new 新会话\n", workspace)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Fprint(os.Stderr, "zgo> ")
		if !sc.Scan() {
			// 超过 1MB 的行曾与 EOF 同路径静默退出 REPL（issue #39）：
			// 丢弃该行并重建 scanner 继续读（ErrTooLong 后 scanner 不可
			// 恢复；重建后继续消费该行剩余部分直到换行）。
			if e := sc.Err(); e != nil {
				fmt.Fprintln(os.Stderr, "\n[输入] 行超过 1MB，已丢弃；请分段粘贴")
				sc = bufio.NewScanner(os.Stdin)
				sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				continue
			}
			fmt.Fprintln(os.Stderr)
			return 0
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "":
			continue
		case "/exit", "/quit", "/q":
			return 0
		case "/compact":
			if err := svc.CompactSession(info.ID); err != nil {
				fmt.Fprintln(os.Stderr, "[压缩失败]", err)
			} else {
				fmt.Fprintln(os.Stderr, "[压缩] 会话已压缩")
			}
			continue
		case "/plan":
			// 服务先翻转、成功后才写本地 flag（issue #39：失败时本地
			// flag 先翻会让下次 /plan 与服务状态反相）。
			on := !planState[info.ID]
			if err := svc.PlanMode(info.ID, on); err != nil {
				fmt.Fprintln(os.Stderr, "[计划模式]", err)
				continue
			}
			planState[info.ID] = on
			if on {
				fmt.Fprintln(os.Stderr, "[计划模式] 已开启（只读）")
			} else {
				fmt.Fprintln(os.Stderr, "[计划模式] 已关闭")
			}
			continue
		case "/new":
			// 旧 session 的 plan flag 随之清理（issue #39：全局 map 只增
			// 不减，长 REPL 会话缓慢泄漏）。
			delete(planState, info.ID)
			var err error
			info, err = svc.NewSession(workspace)
			if err != nil {
				fmt.Fprintln(os.Stderr, "[新建]", err)
				continue
			}
			fmt.Fprintln(os.Stderr, "[新建] 已开启新会话")
			continue
		}
		if err := svc.Chat(info.ID, line, func(ev agent.Event) { render(ev) }); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
		}
		fmt.Println()
	}
}

// planState tracks the REPL's plan-mode flag per session.
var planState = map[string]bool{}
