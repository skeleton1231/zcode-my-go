package main

// REPL 的 raw 模式行编辑器（spec 规则 32）：字符插入/退格（整
// rune）、←/→ 光标、Home/End、↑/↓ 历史召回（与 TUI 同语义：连续重
// 复折叠、离开保存草稿、回最新恢复、边界不动）、Enter 提交、Ctrl+C
// 空行退出/有内容清行。管道 stdin（非终端）由调用方走 Scanner，不进
// 本编辑器。

import (
	"bufio"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// editorState 承载一次编辑会话（多行提交共享历史与终端状态）。
type editorState struct {
	in      *bufio.Reader
	out     io.Writer
	restore func()

	history   []string
	histCur   int // -1 = 实时输入
	histDraft []rune
}

// newEditor 把 stdin 切 raw 模式并启用括号粘贴（spec 规则 33）；stdin
// 非终端时返回 nil（调用方退回 Scanner）。
func newEditor() *editorState {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil
	}
	e := &editorState{
		in:      bufio.NewReader(os.Stdin),
		out:     os.Stderr,
		restore: func() { term.Restore(fd, old) },
	}
	e.out.Write([]byte("\x1b[?2004h")) // 括号粘贴开
	return e
}

// Close 恢复终端（先关括号粘贴）。
func (e *editorState) Close() {
	e.out.Write([]byte("\x1b[?2004l"))
	e.restore()
}

// readLine 编辑一行：返回提交的文本；EOF/io 结束返回 ok=false。
// Ctrl+C：行非空先清行（再按才退出——quit 返回 true 让调用方收尾）。
func (e *editorState) readLine(prompt string) (line string, ok, quit bool) {
	var buf []rune
	cur := 0
	e.histCur, e.histDraft = -1, nil
	e.draw(prompt, buf, cur)
	for {
		r, _, err := e.in.ReadRune()
		if err != nil {
			return "", false, false
		}
		switch r {
		case '\r', '\n':
			e.out.Write([]byte("\r\n"))
			text := strings.TrimRight(string(buf), "\n")
			if t := strings.TrimSpace(text); t != "" {
				if n := len(e.history); n == 0 || e.history[n-1] != t {
					e.history = append(e.history, t)
				}
			}
			return text, true, false
		case 3: // Ctrl+C
			if len(buf) == 0 {
				e.out.Write([]byte("^C\r\n"))
				return "", false, true
			}
			buf, cur = nil, 0
		case 127, 8: // 退格：删光标前一个 rune
			if cur > 0 {
				buf = append(buf[:cur-1], buf[cur:]...)
				cur--
			}
		case 4: // Ctrl+D：空行即 EOF
			if len(buf) == 0 {
				return "", false, false
			}
		default:
			if r >= 0x20 { // 可打印（含多字节 rune）
				buf = append(buf[:cur], append([]rune{r}, buf[cur:]...)...)
				cur++
			} else if r == 27 { // ESC 序列：方向键/Home/End/粘贴
				seq, _, err := e.in.ReadRune()
				if err != nil || seq != '[' {
					continue
				}
				final, _, err := e.in.ReadRune()
				if err != nil {
					continue
				}
				if final == '2' { // 200~/201~ 粘贴标记（spec 规则 33）
					d1, _, err := e.in.ReadRune()
					if err != nil {
						continue
					}
					if d1 == '0' { // 开始标记 200~：再吞 0 与 ~ 后进入粘贴消费
						if d2, _, err := e.in.ReadRune(); err == nil && d2 == '0' {
							e.in.ReadRune() // ~
							text := e.readPasted()
							runes := []rune(text)
							buf = append(buf[:cur], append(runes, buf[cur:]...)...)
							cur += len(runes)
						}
					}
					continue
				}
				switch final {
				case 'D': // ←
					if cur > 0 {
						cur--
					}
				case 'C': // →
					if cur < len(buf) {
						cur++
					}
				case 'H', '1': // Home（后者吞 ; 组合尾）
					if final == '1' {
						e.in.ReadRune() // '~'
					}
					cur = 0
				case 'F', '4': // End
					if final == '4' {
						e.in.ReadRune()
					}
					cur = len(buf)
				case 'A': // ↑ 历史向旧
					e.recall(prompt, &buf, &cur, -1)
				case 'B': // ↓ 历史向新
					e.recall(prompt, &buf, &cur, 1)
				}
			}
		}
		e.draw(prompt, buf, cur)
	}
}

// recall 历史召回（与 tuiState.historyMove 同语义）。
func (e *editorState) recall(prompt string, buf *[]rune, cur *int, dir int) {
	if len(e.history) == 0 {
		return
	}
	at := e.histCur
	if at == -1 {
		if dir > 0 {
			return
		}
		e.histDraft = append([]rune(nil), *buf...)
		at = len(e.history)
	}
	at += dir
	if at < 0 || at > len(e.history) {
		return
	}
	if at == len(e.history) {
		*buf = append([]rune(nil), e.histDraft...)
	} else {
		*buf = []rune(e.history[at])
	}
	e.histCur = at
	*cur = len(*buf)
}

// draw 重绘：\r + 清行 + 提示符 + 内容 + 光标回退到编辑位。
func (e *editorState) draw(prompt string, buf []rune, cur int) {
	e.out.Write([]byte("\r\x1b[K" + prompt + string(buf) +
		"\x1b[" + itoaEd(len(buf)-cur) + "D"))
}

func itoaEd(n int) string {
	if n <= 0 {
		return "1" // 不动（D 参数最小 1）
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// readPasted 消费到粘贴结束标记（ESC[201~），CR/CRLF 归一为换行——
// 粘贴内的回车不触发提交（spec 规则 33）。疑似结束标记的 ESC 序列
// 先入待定缓冲，不是结束标记则原样吐回内容。
func (e *editorState) readPasted() string {
	var b strings.Builder
	var pending []rune
	for {
		r, _, err := e.in.ReadRune()
		if err != nil {
			flushPending(&b, pending)
			return b.String()
		}
		if len(pending) > 0 {
			pending = append(pending, r)
			want := []rune{27, '[', '2', '0', '1', '~'}
			if runesEqualPrefix(pending, want) {
				if len(pending) == len(want) {
					return b.String()
				}
				continue
			}
			flushPending(&b, pending[:len(pending)-1])
			pending = pending[:0]
		} else if r == 27 {
			pending = append(pending, r)
			continue
		}
		switch r {
		case '\r':
			next, _, err := e.in.ReadRune()
			if err == nil && next != '\n' {
				e.in.UnreadRune()
			}
			b.WriteRune('\n')
		case '\n':
			b.WriteRune('\n')
		default:
			b.WriteRune(r)
		}
	}
}

func runesEqualPrefix(got, want []rune) bool {
	for i, r := range got {
		if r != want[i] {
			return false
		}
	}
	return true
}

func flushPending(b *strings.Builder, pending []rune) {
	for _, r := range pending {
		switch r {
		case '\r', '\n':
			b.WriteRune('\n')
		default:
			b.WriteRune(r)
		}
	}
}
