package main

// The full-screen TUI (ZCode's terminal UI, translated to ANSI over a raw
// terminal): an alternate screen with a scrolling transcript, a status
// line and an input line. Pure Go — golang.org/x/term for raw mode.

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

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
}

// runTUI drives the interactive full-screen interface. It returns the
// exit code.
func runTUI(svc *service.AgentService, workspace string) int {
	fd := int(os.Stdin.Fd())
	original, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI 需要交互式终端（-p 走 print 模式）：", err)
		return 2
	}
	defer term.Restore(fd, original)

	s := &tuiState{status: "就绪", redraw: make(chan struct{}, 1)}
	s.resize()
	s.push(fmt.Sprintf("zgo 全屏模式 · 工作区 %s · Ctrl+C 退出 · Enter 发送", workspace))
	s.push("命令：/exit 退出 · /compact 压缩 · /plan 计划模式 · /new 新会话")

	info, err := svc.NewSession(workspace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
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
			s.push(ansi(ansiCyan, "你 > ") + line)
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
		handled, quit := runSlashCommand(&commandEnv{
			svc: svc, info: info, workspace: workspace,
			out: func(msg string) { s.push(msg) },
		}, line)
		if quit {
			s.finish(os.Stdout)
			os.Exit(0)
		}
		if handled {
			s.requestRedraw()
		}
	}()
	if strings.HasPrefix(line, "/") {
		return
	}

	s.setStatus("运行中…", true)
	go func() {
		var last string
		err := svc.Chat(info.ID, line, func(ev agent.Event) { s.applyEvent(ev, &last) })
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
		if ev.IsError {
			s.lines = append(s.lines, ansi(ansiRed, "  ✕ "+firstLine(ev.ResultContent)))
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

// draw renders the alternate screen: transcript, status, input.
func (s *tuiState) draw(out *os.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, h := s.width, s.height
	if w < 10 || h < 5 {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b[?1049h\x1b[2J\x1b[H") // 备用屏 + 清屏 + 回起点
	body := h - 2                             // 状态行 + 输入行
	visible := s.lines
	if len(visible) > body {
		visible = visible[len(visible)-body:]
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
	out.WriteString(b.String())
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
