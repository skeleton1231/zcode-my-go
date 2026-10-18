package main

// The full-screen TUI (ZCode's terminal UI, translated to ANSI over a raw
// terminal): an alternate screen with a scrolling transcript, a status
// line and an input line. Pure Go — golang.org/x/term for raw mode.

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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

	plan   []tools.TodoTask        // 任务面板：todo 工具落盘后刷新
	onPlan func() []tools.TodoTask // 生产读 svc.TodoPlan，测试注入桩

	// 后台代理面板（spec 规则 58）：每次重画前经 onAgents 取快照——
	// 事件触发的重画都带最新状态；空清单不占行。
	onAgents func() []tools.AgentJobView
	agents   []tools.AgentJobView // 最近一次快照（draw 刷新）
	onUsage  func() string        // 生产读会话累计用量（spec 规则 40），测试注入桩
	abort    func()               // 生产读 svc.Abort(activeID)，测试注入桩（spec 规则 38）

	// 会话内搜索（spec 规则 56）：findQ 非空即激活；findHits 是命中行
	// 下标，findCur 是当前命中（渲染 ▶/▶▶ 标记，n/N 跳转，Esc 退出）。
	findQ    string
	findHits []int
	findCur  int

	// 输入历史（spec 规则 30）：已提交的用户消息；histCur==-1 表示在
	// 实时输入（离开时的草稿保存在 histDraft）。
	history   []string
	think     string // 当前回合的思考流缓冲（灰显行）
	histCur   int
	histDraft []rune
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
	defer func() {
		os.Stdout.WriteString("\x1b[?2004l") // 关括号粘贴（spec 规则 33）
		term.Restore(fd, original)
	}()
	os.Stdout.WriteString("\x1b[?2004h") // 开括号粘贴

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
		trustHint(os.Stderr, svc, workspace)
	}
	// 闭包按引用捕获 info：/new 经 SetSession 换 ID 后面板读到新会话。
	s.onPlan = func() []tools.TodoTask { return svc.TodoPlan(info.ID) }
	s.onAgents = func() []tools.AgentJobView { return svc.AgentJobStates(info.ID) }
	s.onUsage = func() string {
		u, err := svc.UsageSummaryOf(info.ID)
		if err != nil || (u.Usage.InputTokens == 0 && u.Usage.OutputTokens == 0) {
			return ""
		}
		return fmt.Sprintf("↑%s ↓%s", tuiTokens(u.Usage.InputTokens+u.Usage.CacheReadTokens+u.Usage.CacheWriteTokens), tuiTokens(u.Usage.OutputTokens))
	}
	// Esc 中止（spec 规则 38）：闭包按引用捕获 info——/resume 换会话
	// 后中止的是当前会话的运行。
	s.abort = func() {
		svc.Abort(info.ID)
		s.setStatus("中止中…", true)
	}
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
			// /mcp 展开后直接发送（spec 规则 28）：转录可见 + 常规
			// 发送路径（含插话判定）。
			send: func(text string) {
				s.pushTurn(text)
				go s.sendChat(svc, info, workspace, text)
			},
			// /retry、/resend（spec 规则 29）：EditAndResend 包装，
			// 事件流照常渲染（旧轮的显示行留在转录上方）。
			rerun: func(text string) error {
				return s.rerunChat(svc, info, text)
			},
			// /find（spec 规则 56）：进入标记模式并跳到最近命中。
			find: func(q string) string { return s.findStart(q) },
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

	// 运行中插话（spec 规则 26）：入队下一回合生效，不打断当前流。
	if steered, notice := maybeSteer(svc, info.ID, line); steered {
		s.pushTurn(line) // 转录可见 + 导航锚点
		s.push(ansi(ansiBlue, notice))
		return
	}

	go s.sendChat(svc, info, workspace, line)
}

// sendChat runs one chat turn（/mcp 展开与常规输入共用；spec 规则 28）。
func (s *tuiState) sendChat(svc *service.AgentService, info *agent.SessionInfo, workspace, line string) {
	s.setStatus("运行中…", true)
	defer func() {
		s.mu.Lock()
		s.running = false
		s.status = "就绪"
		if s.onUsage != nil {
			if u := s.onUsage(); u != "" {
				s.status = "就绪 · " + u
			}
		}
		s.mu.Unlock()
		s.requestRedraw()
	}()
	var last string
	err := svc.ChatMedia(info.ID, line, s.attach.take(), func(ev agent.Event) { s.applyEvent(ev, &last) })
	if err != nil {
		s.push("[错误] " + err.Error())
	}
}

// rerunChat 撤最后一轮并以 text 重发（/retry、/resend 的包装，spec
// 规则 29）：状态与事件渲染同 sendChat；重发文本进转录（旧轮的显示行
// 留在上方，与 CC 的转录观感一致）。
func (s *tuiState) rerunChat(svc *service.AgentService, info *agent.SessionInfo, text string) error {
	s.setStatus("重跑中…", true)
	defer func() {
		s.mu.Lock()
		s.running = false
		s.status = "就绪"
		s.mu.Unlock()
		s.requestRedraw()
	}()
	s.pushTurn(text)
	var last string
	return svc.EditAndResend(info.ID, text, func(ev agent.Event) { s.applyEvent(ev, &last) })
}

// applyEvent folds agent events into transcript lines.
func (s *tuiState) applyEvent(ev agent.Event, last *string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Kind {
	case agent.EvTextDelta:
		*last += ev.Text
		// 思考已展示过：定稿后补回文本流行（zgo 行被 ◌ 顶到上面了）。
		if s.think != "" {
			s.think = ""
			s.lines = append(s.lines, ansi(ansiBold, "zgo > "))
		}
		// 流式：替换最后一条 assistant 行（由 message_start 预置）。
		if len(s.lines) > 0 && strings.HasPrefix(s.lines[len(s.lines)-1], "zgo") {
			s.lines[len(s.lines)-1] = ansi(ansiBold, "zgo > ") + wrap(*last, s.width)
		}
	case agent.EvMessageStart:
		*last = ""
		s.think = ""
		s.lines = append(s.lines, ansi(ansiBold, "zgo > "))
	case agent.EvThinkingDelta:
		// 思考流灰显（spec 规则 40）：累积渲染为「◌ …」行；首条文本
		// delta 到达即定稿（不再更新）。
		s.think += ev.Text
		if n := len(s.lines); n == 0 || !strings.HasPrefix(s.lines[n-1], ansi(ansiDim, "◌")) {
			s.lines = append(s.lines, ansi(ansiDim, "◌ "))
		}
		n := len(s.lines)
		s.lines[n-1] = ansi(ansiDim, "◌ "+wrap(s.think, s.width))
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
	case agent.EvBudgetNotice:
		// 用户可见通知（spec 规则 36）：黄显提示行，不进转录语义。
		s.lines = append(s.lines, ansi(ansiYellow, "[预算] "+ev.Text))
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
	s.pushHistory(line)
	s.histCur, s.histDraft = -1, nil
	s.mu.Unlock()
	s.requestRedraw()
}

// findStart 开始会话内搜索（/find 钩子的 TUI 实现，spec 规则 56）：
// 大小写不敏感匹配转录行，跳到最近一处命中；状态行给出跳转键提示。
func (s *tuiState) findStart(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return s.findClear()
	}
	s.mu.Lock()
	lower := strings.ToLower(q)
	hits := make([]int, 0, 8)
	for i, ln := range s.lines {
		if strings.Contains(strings.ToLower(ln), lower) {
			hits = append(hits, i)
		}
	}
	s.findQ, s.findHits, s.findCur = q, hits, len(hits)-1
	if len(hits) > 0 {
		s.topIdx = hits[s.findCur]
	}
	s.requestRedraw()
	s.mu.Unlock()
	if len(hits) == 0 {
		return "未找到 " + q
	}
	return fmt.Sprintf("找到 %d 处 · n 下一处 · N 上一处 · Esc 退出搜索", len(hits))
}

// findMove 在命中间跳转（环绕），并把视口顶行对准当前命中。
func (s *tuiState) findMove(dir int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.findHits) == 0 {
		return
	}
	s.findCur = (s.findCur + dir + len(s.findHits)) % len(s.findHits)
	s.topIdx = s.findHits[s.findCur]
	s.requestRedraw()
}

// findClear 退出搜索（标记清空、恢复尾随视口）。
func (s *tuiState) findClear() string {
	s.mu.Lock()
	s.findQ, s.findHits, s.findCur = "", nil, -1
	s.requestRedraw()
	s.mu.Unlock()
	return "已退出搜索"
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
	if s.onAgents != nil {                    // 后台代理快照：重画即取（事件驱动的重画都带最新，规则 58）
		s.agents = s.onAgents()
	}
	plan := planLines(s.plan, planMaxRows)
	agents := agentPanelLines(s.agents)
	for _, line := range append(plan, agents...) {
		b.WriteString(line + "\x1b[K\n")
	}
	body := h - 2 - len(plan) - len(agents) // 状态行 + 输入行 + 计划/代理块
	if body < 1 {
		body = 1
	}
	var visible []string
	base := 0          // 可见窗口首行的绝对下标（搜索标记用，spec 规则 56）
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
		base = top
		visible = s.lines[top:end]
	} else if len(s.lines) > body { // 尾随：贴底窗口
		base = len(s.lines) - body
		visible = s.lines[base:]
	} else {
		visible = s.lines
	}
	hitSet := map[int]bool{}
	if s.findQ != "" {
		for _, h := range s.findHits {
			hitSet[h] = true
		}
	}
	for i, line := range visible {
		if s.findQ != "" {
			switch {
			case s.findCur >= 0 && base+i == s.findHits[s.findCur]:
				line = ansi(ansiYellow, ">") + line
			case hitSet[base+i]:
				line = ansi(ansiYellow, ". ") + line
			}
		}
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

// histMax 封顶输入历史条数（issue #192：仓库"一切缓冲有界"的教义——
// 粘贴场景单行可数十 KB，无界历史让长会话缓慢累积；超限丢最旧 FIFO）。
const histMax = 500

// pasteCapRunes 粘贴封顶（issue #195：对齐 stdin 管道 60K 口径——数 MB
// 剪贴板直入上下文与 JSONL 行是 8MB 家族的交互形态）。
const pasteCapRunes = 60 << 10

// capPasted 把输入区压到 pasteCapRunes rune 内并留尾注（粘贴结束时调）。
func capPasted(s *tuiState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.input) <= pasteCapRunes {
		return
	}
	total := len(s.input)
	note := []rune("\n…（粘贴超长已截断，原 " + strconv.Itoa(total) + " 字符）")
	s.input = append(append([]rune(nil), s.input[:pasteCapRunes]...), note...)
}

// pushHistory 记一条历史（调用方持 mu）：连续重复折叠（spec 规则 30），
// 超过 histMax 丢最旧。
func (s *tuiState) pushHistory(line string) {
	if n := len(s.history); n == 0 || s.history[n-1] != line {
		s.history = append(s.history, line)
	}
	if over := len(s.history) - histMax; over > 0 {
		s.history = append([]string(nil), s.history[over:]...)
	}
}

// historyMove 在输入历史中召回（spec 规则 30）：↑ 向旧、↓ 向新；首
// 次离开实时输入时保存草稿，回到最新一行恢复草稿；边界不动；空历史
// 无操作。
func (s *tuiState) historyMove(dir int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) == 0 {
		return
	}
	at := s.histCur
	if at == -1 {
		if dir > 0 {
			return // 已在最新：↓ 无操作
		}
		s.histDraft = append([]rune(nil), s.input...) // 保存草稿
		at = len(s.history)
	}
	at += dir
	if at < 0 || at > len(s.history) {
		return // 边界：不动
	}
	if at == len(s.history) {
		s.input = append([]rune(nil), s.histDraft...) // 回到最新：恢复草稿
	} else {
		s.input = []rune(s.history[at])
	}
	s.histCur = at
	s.requestRedraw()
}

// abortRun 处理裸 Esc（spec 规则 38）：运行中触发 abort 钩子并提示；
// 空闲无操作（不退出、不清输入）。
func (s *tuiState) abortRun() {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running || s.abort == nil {
		return
	}
	s.abort()
	s.push(ansi(ansiYellow, "[已中止] Esc 中断了当前回合"))
	s.requestRedraw()
}

// tuiTokens 折算 token 数（与桌面 formatTokens 同形：k/万级缩写）。
func tuiTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// agentPanelLines 渲染后台代理固定块（spec 规则 58，slash.FormatAgents
// 同数据源）：运行中 ▶ 带耗时；空清单不占行；超 4 条折叠 …+N。
func agentPanelLines(views []tools.AgentJobView) []string {
	if len(views) == 0 {
		return nil
	}
	running := 0
	for _, v := range views {
		if v.State == tools.AgentRunning {
			running++
		}
	}
	lines := []string{ansi(ansiBlue, fmt.Sprintf("代理 · %d 运行/%d", running, len(views)))}
	for i, v := range views {
		if i == agentPanelMaxRows {
			lines = append(lines, ansi(ansiGray, fmt.Sprintf("  …+%d", len(views)-agentPanelMaxRows)))
			break
		}
		mark := "?"
		switch v.State {
		case tools.AgentRunning:
			mark = "▶"
		case tools.AgentDone:
			mark = "✓"
		case tools.AgentFailed:
			mark = "✕"
		case tools.AgentStopped:
			mark = "■"
		}
		line := "  " + mark + " " + v.ID
		if v.State == tools.AgentRunning {
			line += " " + v.Duration.Round(time.Second).String()
		}
		if task := agentTaskBrief(v.Prompt); task != "" {
			line += " " + task
		}
		lines = append(lines, line)
	}
	return lines
}

// agentPanelMaxRows 是代理面板最多渲染的条数。
const agentPanelMaxRows = 4

// agentTaskBrief 任务首行截短。
func agentTaskBrief(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 32 {
		return string(r[:32]) + "…"
	}
	return s
}
