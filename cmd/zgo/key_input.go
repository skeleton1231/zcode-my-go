package main

// keyStream 把 raw 终端字节流转成输入区内容与控制信号（issue #70）：
// 逐字节 rune(b) 曾把中文拆成 Latin-1 乱码、方向键原样进输入区。
// 跨 Read 的多字节字符与 CSI/SS3 转义序列在内部缓冲拼接后再消费。

import (
	"strings"
	"unicode/utf8"
)

// 控制信号（与终端控制字节一致）。
const (
	keyQuit = 3 // Ctrl+C
	keyEOF  = 4 // Ctrl+D
	keyNone = 0
)

// 导航序列（CSI）：Ctrl+↑/↓ 跳用户回合（tuiState.jumpTurn），PageUp/
// PageDown 半屏滚动（tuiState.pageScroll）；其余转义序列照旧整段吞掉。
const (
	seqCtrlUp   = "\x1b[1;5A"
	seqCtrlDown = "\x1b[1;5B"
	seqUp       = "\x1b[A"
	seqDown     = "\x1b[B"
	seqPageUp   = "\x1b[5~"
	seqPageDown = "\x1b[6~"
)

// keyStream 持有跨 Read 的未完成序列。
type keyStream struct {
	buf []byte
	// 括号粘贴（spec 规则 33）：200~/201~ 之间置位，CR/LF 折叠空格。
	pasting bool
}

// feed 消费一段读入的字节，返回控制信号；提交的行经 onLine 交付。
func (k *keyStream) feed(s *tuiState, p []byte) int {
	return k.feedWith(s, p, func(string) {})
}

// feedWith 同 feed，可指定行提交回调。
func (k *keyStream) feedWith(s *tuiState, p []byte, onLine func(string)) int {
	k.buf = append(k.buf, p...)
	for len(k.buf) > 0 {
		b := k.buf[0]
		switch {
		case b == keyQuit, b == keyEOF:
			k.buf = k.buf[1:]
			return int(b)
		case b == '\r' || b == '\n':
			k.buf = k.buf[1:]
			if k.pasting { // 粘贴内的换行：CRLF 折叠为一个空格（单行输入区）
				if b == '\r' && len(k.buf) > 0 && k.buf[0] == '\n' {
					k.buf = k.buf[1:]
				}
				s.mu.Lock()
				s.input = append(s.input, ' ')
				s.mu.Unlock()
				continue
			}
			s.mu.Lock()
			line := strings.TrimSpace(string(s.input))
			s.input = s.input[:0]
			s.mu.Unlock()
			onLine(line)
		case b == 127 || b == 8: // 退格：删整个 rune（s.input 是 []rune）
			k.buf = k.buf[1:]
			s.mu.Lock()
			if n := len(s.input); n > 0 {
				s.input = s.input[:n-1]
			}
			s.mu.Unlock()
		case b == 0x1b: // 转义序列：粘贴标记优先，导航键派发，其余吞掉
			n := escLen(k.buf)
			if n == 0 {
				return keyNone
			}
			seq := string(k.buf[:n])
			if k.handlePasteMarker(seq) {
				k.buf = k.buf[n:]
				continue
			}
			if k.pasting {
				// 粘贴内的其他转义序列：原样入输入区（保真优先）。
				s.mu.Lock()
				s.input = append(s.input, []rune(seq)...)
				s.mu.Unlock()
				k.buf = k.buf[n:]
				continue
			}
			k.dispatchNav(s, seq)
			k.buf = k.buf[n:]
		case b < 0x80:
			k.buf = k.buf[1:]
			k.appendInput(s, rune(b))
		default: // 多字节 UTF-8
			r, size := utf8.DecodeRune(k.buf)
			if r == utf8.RuneError && size == 1 && !utf8.FullRune(k.buf) {
				return keyNone // 跨 Read 的不完整序列，等待
			}
			k.buf = k.buf[size:]
			k.appendInput(s, r)
		}
	}
	return keyNone
}

func (k *keyStream) appendInput(s *tuiState, r rune) {
	s.mu.Lock()
	s.input = append(s.input, r)
	s.mu.Unlock()
}

// dispatchNav 把识别出的导航序列派发到视口状态；未识别的序列无操作。
func (k *keyStream) dispatchNav(s *tuiState, seq string) {
	switch seq {
	case seqCtrlUp:
		s.jumpTurn(-1)
	case seqCtrlDown:
		s.jumpTurn(1)
	case seqPageUp:
		s.pageScroll(-1)
	case seqPageDown:
		s.pageScroll(1)
	case seqUp:
		s.historyMove(-1)
	case seqDown:
		s.historyMove(1)
	}
}

// escLen 返回 buf 开头转义序列的长度（含 ESC），0 表示不完整。
// 覆盖 CSI（ESC[…final）、SS3（ESC O final）与 ESC+单字母旧式序列；
// 裸 ESC 视为 1 字节序列吞掉。
func escLen(buf []byte) int {
	if len(buf) < 2 {
		return 0
	}
	switch buf[1] {
	case '[': // CSI：参数字节后以 0x40-0x7E 结束
		for i := 2; i < len(buf); i++ {
			if buf[i] >= 0x40 && buf[i] <= 0x7E {
				return i + 1
			}
		}
		return 0
	case 'O': // SS3：ESC O + 一个字母
		if len(buf) < 3 {
			return 0
		}
		return 3
	default:
		if buf[1] >= 0x40 && buf[1] <= 0x7E {
			return 2 // ESC+单字母
		}
		return 1 // 裸 ESC
	}
}

// handlePasteMarker 识别 200~/201~ 并切换粘贴态（spec 规则 33）。
func (k *keyStream) handlePasteMarker(seq string) bool {
	const start = "\x1b[200~"
	const end = "\x1b[201~"
	switch seq {
	case start:
		k.pasting = true
		return true
	case end:
		k.pasting = false
		return true
	}
	return false
}
