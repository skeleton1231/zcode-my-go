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

	// 输入循环：raw 键盘。
	buf := make([]byte, 32)
	inputErr := error(nil)
	for inputErr == nil {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			inputErr = err
			break
		}
		for _, b := range buf[:n] {
			switch {
			case b == 3: // Ctrl+C
				s.finish(os.Stdout)
				return 0
			case b == 4: // Ctrl+D
				inputErr = fmt.Errorf("eof")
			case b == '\r' || b == '\n':
				line := strings.TrimSpace(string(s.input))
				s.mu.Lock()
				s.input = s.input[:0]
				s.mu.Unlock()
				if line == "" {
					s.requestRedraw()
					continue
				}
				s.push("你 > " + line)
				s.handleLine(line, svc, &info, workspace)
			case b == 127 || b == 8: // 退格
				s.mu.Lock()
				if len(s.input) > 0 {
					s.input = s.input[:len(s.input)-1]
				}
				s.mu.Unlock()
				s.requestRedraw()
			case b >= 0x20 && b < 0x7f:
				s.mu.Lock()
				s.input = append(s.input, rune(b))
				s.mu.Unlock()
				s.requestRedraw()
			default:
				// 方向键等转义序列与中文多字节：读整段字节再解码。
				s.mu.Lock()
				s.input = append(s.input, rune(b))
				s.mu.Unlock()
				s.requestRedraw()
			}
		}
	}
	_ = inputErr
	s.finish(os.Stdout)
	return 0
}

// handleLine runs one submitted line (commands or a chat turn).
func (s *tuiState) handleLine(line string, svc *service.AgentService, info *agent.SessionInfo, workspace string) {
	switch line {
	case "/exit", "/quit", "/q":
		s.finish(os.Stdout)
		os.Exit(0)
	case "/compact":
		s.setStatus("压缩中…", true)
		go func() {
			err := svc.CompactSession(info.ID)
			s.mu.Lock()
			if err != nil {
				s.push("[压缩失败] " + err.Error())
			} else {
				s.push("[压缩] 会话已压缩")
			}
			s.running = false
			s.status = "就绪"
			s.mu.Unlock()
			s.requestRedraw()
		}()
		return
	case "/plan":
		on := !planState[info.ID]
		planState[info.ID] = on
		if err := svc.PlanMode(info.ID, on); err == nil {
			s.push("[计划模式] " + map[bool]string{true: "已开启（只读）", false: "已关闭"}[on])
		}
		s.requestRedraw()
		return
	case "/new":
		if fresh, err := svc.NewSession(workspace); err == nil {
			*info = fresh
			s.push("[新建] 已开启新会话")
		}
		s.requestRedraw()
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
			s.lines[len(s.lines)-1] = "zgo > " + wrap(*last, s.width)
		}
	case agent.EvMessageStart:
		*last = ""
		s.lines = append(s.lines, "zgo > ")
	case agent.EvMessageEnd:
		if ev.StopReason == "error" && ev.ErrorMessage != "" {
			s.lines = append(s.lines, "[错误] "+ev.ErrorMessage)
		}
	case agent.EvToolExecutionStart:
		s.lines = append(s.lines, "  ⚙ "+ev.ToolName+" "+oneLine(ev.Args))
	case agent.EvToolExecutionEnd:
		if ev.IsError {
			s.lines = append(s.lines, "  ✕ "+firstLine(ev.ResultContent))
		}
	case agent.EvCompaction:
		s.lines = append(s.lines, "[压缩] "+ev.Text)
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
	if s.running {
		status = "● " + status
	}
	b.WriteString("\x1b[7m " + status + strings.Repeat(" ", max(0, w-2-len([]rune(status)))) + " \x1b[0m\n")
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
