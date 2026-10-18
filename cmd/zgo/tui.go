package main

// The full-screen TUI (ZCode's terminal UI, translated to ANSI over a raw
// terminal): an alternate screen with a scrolling transcript, a status
// line and an input line. Pure Go — golang.org/x/term for raw mode.

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// planMaxRows 是任务面板固定块最多渲染的任务条数（超出折叠 …+N）。
const planMaxRows = 6

// ANSI 前景码：颜色只加在最终字符串外层（wrap 已完成），不参与宽度计算。
const (
	ansiBold   = "1"
	ansiDim    = "2"
	ansiRed    = "31"
	ansiGreen  = "32"
	ansiYellow = "33"
	ansiBlue   = "34"
	ansiCyan   = "36"
	ansiGray   = "90"
)

// colorEnabled 遵循 NO_COLOR 惯例（置任意值即禁色）。
var colorEnabled = os.Getenv("NO_COLOR") == ""

// ansi wraps s in a color when enabled.
func ansi(code, s string) string {
	if !colorEnabled {
		return s
	}
	// 显式 \x1b 转义而非内嵌裸 ESC 字节（issue #66）：裸字节不可见、
	// 易被编辑器静默剥离，已误导过一次静态审查。
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

type tuiState struct {
	mu      sync.Mutex
	lines   []string // transcript lines (wrapped before storing)
	input   []rune
	status  string
	running bool
	width   int
	height  int
	redraw  chan struct{}

	attach attachments // /attach 攒图，随下一条消息发送

	// 回合导航（ZCode task-navigation 的 TUI 面）：topIdx 是视口首行的绝
	// 对下标，-1 表示尾随最新（零值 0 会钉在转录顶部，构造方必须显式置
	// -1）；turns 登记用户回合行下标，turnCur 是回合游标（-1 表示在最新）。
	topIdx  int
	turns   []int
	turnCur int

	plan   []tools.TodoTask     // 任务面板：todo 工具落盘后刷新
	onPlan func() []tools.TodoTask // 生产读 svc.TodoPlan，测试注入桩
}

// runTUI drives the interactive full-screen interface. It returns the
// exit code. resumed 时沿用传入会话并回放转录（--continue/--resume 的
// TUI 形态），否则在工作区新建。
func runTUI(svc *service.AgentService, workspace string, resumeInfo agent.SessionInfo, resumed bool) int {
	fd := int(os.Stdin.Fd())
	original, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI 需要交互式终端（-p 走 print 模式）：", err)
		return 2
	}
	defer term.Restore(fd, original)

	s := &tuiState{status: "就绪", redraw: make(chan struct{}, 1), topIdx: -1, turnCur: -1}
	s.resize()
	info := resumeInfo
	if resumed {
		// 恢复会话沿用其自身工作区（即使与当前目录不同）。
		workspace = info.Workspace
		s.push(fmt.Sprintf("zgo 全屏模式 · 恢复会话 %s · %s · Ctrl+C 退出", shortID(info.ID), info.Title))
		if msgs, merr := svc.Messages(info.ID); merr == nil {
			replayHistory(s, msgs)
		} else {
			s.push(ansi(ansiRed, "[恢复失败] "+merr.Error()))
		}
	} else {
		s.push(fmt.Sprintf("zgo 全屏模式 · 工作区 %s · Ctrl+C 退出 · Enter 发送", workspace))
	}
	s.push("命令：/exit 退出 · /compact 压缩 · /plan 计划模式 · /new 新会话")

	if !resumed {
		info, err = svc.NewSession(workspace)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
	}
	// 闭包按引用捕获 info：/new 经 SetSession 换 ID 后面板读到新会话。
	s.onPlan = func() []tools.TodoTask { return svc.TodoPlan(info.ID) }
	svc.SetAsker(stdinAsker)
	svc.SetQuestion(stdinQuestion)

	// 绘制协程：状态变化即重画。
	drawDone := make(chan struct{})
	go func() {
		defer close(drawDone)
		for range s.redraw {
			s.draw(os.Stdout)
		}
	}()
	s.requestRedraw()

	// 输入循环：raw 键盘。keyStream 处理多字节与转义序列的跨 Read
	// 拼接（issue #70：逐字节 rune 化曾让中文与方向键全坏）。
	buf := make([]byte, 32)
	keys := &keyStream{}
	inputErr := error(nil)
	for inputErr == nil {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			inputErr = err
			break
		}
		switch keys.feedWith(s, buf[:n], func(line string) {
			if line == "" {
				return
			}
			s.pushTurn(line)
			s.handleLine(line, svc, &info, workspace)
		}) {
		case keyQuit:
			s.finish(os.Stdout)
			return 0
		case keyEOF:
			inputErr = fmt.Errorf("eof")
		}
		s.requestRedraw()
	}
	_ = inputErr
	s.finish(os.Stdout)
	return 0
}

// handleLine runs one submitted line (commands or a chat turn).
func (s *tuiState) handleLine(line string, svc *service.AgentService, info *agent.SessionInfo, workspace string) {
	// 命令在后台执行（/compact 等是网络调用，同步会卡住输入循环——
	// 原 TUI 实现即为异步）；push 自带锁。
	go func() {
		prevID := info.ID
		handled, quit := runSlashCommand(&commandEnv{
			svc: svc, info: info, workspace: workspace,
			out:    func(msg string) { s.push(msg) },
			attach: &s.attach,
		}, line)
		if quit {
			s.finish(os.Stdout)
			os.Exit(0)
		}
		if handled {
			if info.ID != prevID { // /new 或 /resume 换了会话：旧回合锚点与计划作废
				s.resetSessionView()
				// /resume 切到既有会话：回放目标转录（/new 的空会话为
				// 天然 no-op），回合照常登记导航锚点。
				if msgs, err := svc.Messages(info.ID); err == nil && len(msgs) > 0 {
					s.push(ansi(ansiCyan, fmt.Sprintf("— 已切换到会话 %s · %s —", shortID(info.ID), info.Title)))
					replayHistory(s, msgs)
				}
			}
			s.requestRedraw()
		}
	}()
	if strings.HasPrefix(line, "/") {
		return
	}

	s.setStatus("运行中…", true)
	go func() {
		var last string
		err := svc.ChatMedia(info.ID, line, s.attach.take(), func(ev agent.Event) { s.applyEvent(ev, &last) })
		s.mu.Lock()
		s.running = false
		s.status = "就绪"
		if err != nil {
			s.push("[错误] " + err.Error())
		}
		s.mu.Unlock()
		s.requestRedraw()
	}()
}

// applyEvent folds agent events into transcript lines.
func (s *tuiState) applyEvent(ev agent.Event, last *string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Kind {
	case agent.EvTextDelta:
		*last += ev.Text
		// 流式：替换最后一条 assistant 行（由 message_start 预置）。
		if len(s.lines) > 0 && strings.HasPrefix(s.lines[len(s.lines)-1], "zgo") {
			s.lines[len(s.lines)-1] = ansi(ansiBold, "zgo > ") + wrap(*last, s.width)
		}
	case agent.EvMessageStart:
		*last = ""
		s.lines = append(s.lines, ansi(ansiBold, "zgo > "))
	case agent.EvMessageEnd:
		if ev.StopReason == "error" && ev.ErrorMessage != "" {
			s.lines = append(s.lines, ansi(ansiRed, "[错误] "+ev.ErrorMessage))
		}
	case agent.EvToolExecutionStart:
		s.lines = append(s.lines, ansi(ansiGray, "  ⚙ "+ev.ToolName+" "+oneLine(ev.Args)))
	case agent.EvToolExecutionEnd:
		// 任务面板（规则 18）：todo 落盘即刷新计划快照。
		if ev.ToolName == "todo" && s.onPlan != nil {
			s.plan = s.onPlan()
		}
		if ev.IsError {
			s.lines = append(s.lines, ansi(ansiRed, "  ✕ "+firstLine(ev.ResultContent)))
		}
		for _, dl := range strings.Split(strings.TrimSuffix(ev.Diff, "\n"), "\n") {
			if dl == "" {
				continue
			}
			switch {
			case strings.HasPrefix(dl, "+"):
				s.lines = append(s.lines, ansi(ansiGreen, "  "+dl))
			case strings.HasPrefix(dl, "-"):
				s.lines = append(s.lines, ansi(ansiRed, "  "+dl))
			default:
				s.lines = append(s.lines, ansi(ansiGray, "  "+dl))
			}
		}
	case agent.EvCompaction:
		s.lines = append(s.lines, ansi(ansiBlue, "[压缩] "+ev.Text))
	}
	s.requestRedraw()
}

func (s *tuiState) setStatus(status string, running bool) {
	s.mu.Lock()
	s.status = status
	s.running = running
	s.mu.Unlock()
	s.requestRedraw()
}

func (s *tuiState) push(line string) {
	s.mu.Lock()
	s.lines = append(s.lines, wrap(line, s.width))
	s.mu.Unlock()
	s.requestRedraw()
}

// pushTurn 记录一个用户回合行并登记回合导航锚点；新回合即回到最新。
func (s *tuiState) pushTurn(line string) {
	s.mu.Lock()
	s.lines = append(s.lines, wrap(ansi(ansiCyan, "你 > ")+line, s.width))
	s.turns = append(s.turns, len(s.lines)-1)
	s.turnCur = -1
	s.mu.Unlock()
	s.requestRedraw()
}

// resetSessionView 清空回合锚点与计划（/new 换会话后旧数据不再有意义）；
// 转录行保留（跨会话历史本就同屏滚动）。
func (s *tuiState) resetSessionView() {
	s.mu.Lock()
	s.turns, s.turnCur, s.topIdx, s.plan = nil, -1, -1, nil
	s.mu.Unlock()
	s.requestRedraw()
}

// bodyRows 是转录可用行数（扣状态行与输入行；调用方持锁或独占）。
func (s *tuiState) bodyRows() int {
	body := s.height - 2
	if body < 1 {
		body = 1
	}
	return body
}

// jumpTurn 在用户回合间跳转（桌面 Ctrl+↑/↓ 同语义）：从尾随态首次向上
// 跳到最后一轮；到最早回合再向上不动；向下越过最新一轮恢复尾随。
func (s *tuiState) jumpTurn(dir int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.turns) == 0 {
		return
	}
	at := s.turnCur
	if at < 0 || at >= len(s.turns) {
		at = len(s.turns) // 游标在「最新（尾随）」位
	}
	at += dir
	if at < 0 || at > len(s.turns) {
		return // 边界：不动
	}
	if at == len(s.turns) { // 越过最新 → 恢复尾随
		s.topIdx, s.turnCur = -1, -1
		s.requestRedraw()
		return
	}
	s.turnCur = at
	body := s.bodyRows()
	top := s.turns[at]
	if top+body > len(s.lines) {
		top = len(s.lines) - body
	}
	if top < 0 {
		top = 0
	}
	s.topIdx = top
	s.requestRedraw()
}

// pageScroll 半屏滚动（PageUp/PageDown）；滚到底恢复尾随，其余夹紧。
func (s *tuiState) pageScroll(dir int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body := s.bodyRows()
	if len(s.lines) <= body {
		return
	}
	top := s.topIdx
	if top < 0 {
		top = len(s.lines) - body // 尾随态的窗口顶
	}
	top += dir * body / 2
	if top < 0 {
		top = 0
	}
	if top > len(s.lines)-body {
		if dir > 0 { // 向下滚到底 → 尾随
			s.topIdx = -1
			s.requestRedraw()
			return
		}
		top = len(s.lines) - body
	}
	s.topIdx = top
	s.requestRedraw()
}

// planLines 渲染任务面板固定块（标记与桌面 planView 同一套：▶/✓/□）。
func planLines(plan []tools.TodoTask, maxRows int) []string {
	if len(plan) == 0 {
		return nil
	}
	done := 0
	for _, t := range plan {
		if t.Status == tools.TodoDone {
			done++
		}
	}
	lines := []string{ansi(ansiBlue, fmt.Sprintf("计划 · %d/%d", done, len(plan)))}
	for i, t := range plan {
		if i == maxRows {
			lines = append(lines, ansi(ansiGray, fmt.Sprintf("  …+%d", len(plan)-maxRows)))
			break
		}
		mark := "□"
		switch t.Status {
		case tools.TodoInProgress:
			mark = "▶"
		case tools.TodoDone:
			mark = "✓"
		}
		lines = append(lines, ansi(ansiGray, "  "+mark+" "+t.Content))
	}
	return lines
}

func (s *tuiState) requestRedraw() {
	select {
	case s.redraw <- struct{}{}:
	default:
	}
}

func (s *tuiState) resize() {
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		s.mu.Lock()
		s.width, s.height = w, h
		s.mu.Unlock()
	}
}

// draw renders the alternate screen: plan block, transcript, status, input.
// out 收 io.Writer 以便无终端测试。
func (s *tuiState) draw(out io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, h := s.width, s.height
	if w < 10 || h < 5 {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b[?1049h\x1b[2J\x1b[H") // 备用屏 + 清屏 + 回起点
	plan := planLines(s.plan, planMaxRows)
	for _, line := range plan {
		b.WriteString(line + "\x1b[K\n")
	}
	body := h - 2 - len(plan) // 状态行 + 输入行 + 计划块
	if body < 1 {
		body = 1
	}
	var visible []string
	if s.topIdx >= 0 { // 回合导航/滚动：绝对下标窗口，夹紧防越界
		top := s.topIdx
		if maxTop := len(s.lines) - body; top > maxTop {
			top = maxTop
		}
		if top < 0 {
			top = 0
		}
		end := top + body
		if end > len(s.lines) {
			end = len(s.lines)
		}
		visible = s.lines[top:end]
	} else if len(s.lines) > body { // 尾随：贴底窗口
		visible = s.lines[len(s.lines)-body:]
	} else {
		visible = s.lines
	}
	for _, line := range visible {
		b.WriteString(line + "\x1b[K\n")
	}
	for i := len(visible); i < body; i++ {
		b.WriteString("\x1b[K\n")
	}
	status := s.status
	plain := status
	if s.running {
		plain = "● " + status
		// 宽度按可见字符计：转义码不计入，否则反显条右缘留残迹。
		status = ansi(ansiGreen, plain)
	}
	b.WriteString("\x1b[7m " + status + strings.Repeat(" ", max(0, w-2-len([]rune(plain)))) + " \x1b[0m\n")
	b.WriteString("> " + string(s.input) + "\x1b[K")
	io.WriteString(out, b.String())
}

// finish restores the primary screen.
func (s *tuiState) finish(out *os.File) {
	out.WriteString("\x1b[?1049l")
}

func wrap(line string, width int) string {
	if width <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= width-1 {
		return line
	}
	var b strings.Builder
	for len(runes) > width-1 {
		b.WriteString(string(runes[:width-1]))
		b.WriteString("↵")
		runes = runes[width-1:]
	}
	b.WriteString(string(runes))
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// replayHistory 把既有会话的消息回放进转录（--continue/--resume 的 TUI
// 形态）：用户回合经 pushTurn 登记导航锚点（恢复的回合一样可 Ctrl+↑/↓
// 跳转），助手文本走常规样式；工具块只保留文本部分（历史里的调用记
// 录以结果文本呈现）。返回回放的回合数。
func replayHistory(s *tuiState, msgs []ai.Message) int {
	turns := 0
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleUser:
			if txt := m.Text(); txt != "" {
				s.pushTurn(txt)
				turns++
			}
		case ai.RoleAssistant:
			if txt := m.Text(); txt != "" {
				s.push(ansi(ansiBold, "zgo > ") + txt)
			}
		}
	}
	return turns
}
