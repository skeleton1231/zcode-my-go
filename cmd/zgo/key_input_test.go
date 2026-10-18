package main

import (
	"fmt"
	"testing"
)

// TestKeyStreamChineseInput 验证多字节 UTF-8 跨 Read 拼接（issue #70：
// 逐字节 rune(b) 曾把中文拆成 Latin-1 乱码，全屏模式对中文不可用）。
func TestKeyStreamChineseInput(t *testing.T) {
	s := &tuiState{}
	k := &keyStream{}
	// "你" 的 3 字节分两段到达（模拟跨 Read 边界）。
	all := []byte("你好")
	k.feed(s, all[:1])
	if n := inputLen(s); n != 0 {
		t.Fatalf("partial rune flushed: %d", n)
	}
	k.feed(s, all[1:3])
	if got := inputText(s); got != "你" {
		t.Fatalf("input = %q", got)
	}
	// "好" 完整到达。
	k.feed(s, all[3:])
	if got := inputText(s); got != "你好" {
		t.Fatalf("input = %q", got)
	}
}

// TestKeyStreamEscapeSwallowed 验证方向键等 CSI 序列不进输入区（issue
// #70：ESC[D 曾变成三个 rune 原样入输入区）。
func TestKeyStreamEscapeSwallowed(t *testing.T) {
	s := &tuiState{}
	k := &keyStream{}
	k.feed(s, []byte("a"))
	k.feed(s, []byte{0x1b, '[', 'D'}) // ← 左方向键
	k.feed(s, []byte{0x1b, 'O', 'A'}) // ↑ SS3 形态
	k.feed(s, []byte("b"))
	if got := inputText(s); got != "ab" {
		t.Fatalf("input = %q", got)
	}
}

// TestKeyStreamControls 验证退格删整 rune、Enter 提交、Ctrl+C/D 信号。
func TestKeyStreamControls(t *testing.T) {
	s := &tuiState{}
	k := &keyStream{}
	var lines []string
	submit := func(line string) { lines = append(lines, line) }
	k.feedWith(s, []byte("hi你"), submit)
	if got := inputText(s); got != "hi你" {
		t.Fatalf("input = %q", got)
	}
	k.feedWith(s, []byte{127}, submit) // 退格删掉整个"你"
	if got := inputText(s); got != "hi" {
		t.Fatalf("after backspace = %q", got)
	}
	k.feedWith(s, []byte("\r"), submit)
	if len(lines) != 1 || lines[0] != "hi" {
		t.Fatalf("lines = %v", lines)
	}
	if got := (&keyStream{}).feedWith(s, []byte{3}, submit); got != keyQuit {
		t.Fatalf("ctrl-c = %v", got)
	}
	if got := (&keyStream{}).feedWith(s, []byte{4}, submit); got != keyEOF {
		t.Fatalf("ctrl-d = %v", got)
	}
}

func inputLen(s *tuiState) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.input)
}

func inputText(s *tuiState) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.input)
}

// TestKeyStreamNavigationKeys 验证 Ctrl+↑/↓ 与 PageUp/PageDown 序列驱动
// 视口导航且不进输入区（其余转义序列照旧整段吞掉）。
func TestKeyStreamNavigationKeys(t *testing.T) {
	s := &tuiState{width: 40, height: 12, topIdx: -1, turnCur: -1}
	s.pushTurn("q1")
	s.push("r1")
	s.pushTurn("q2")
	for i := 0; i < 28; i++ {
		s.push(fmt.Sprintf("filler %d", i))
	}
	k := &keyStream{}

	k.feed(s, []byte(seqCtrlUp))
	if s.topIdx != 2 { // 最后一轮 q2 在第 2 行
		t.Fatalf("ctrlUp: topIdx=%d, want 2", s.topIdx)
	}
	k.feed(s, []byte(seqCtrlDown))
	k.feed(s, []byte(seqCtrlDown))
	if s.topIdx != -1 {
		t.Fatalf("ctrlDown to tail: topIdx=%d, want -1", s.topIdx)
	}

	k.feed(s, []byte(seqPageUp))
	if s.topIdx != 16 { // 31 行、body=10：尾随窗顶 21，半屏上移 5
		t.Fatalf("pageUp: topIdx=%d, want 16", s.topIdx)
	}
	k.feed(s, []byte(seqPageDown))
	if s.topIdx != 21 { // 回到窗顶 21（== len-body，再向下即到底恢复尾随）
		t.Fatalf("pageDown: topIdx=%d, want 21", s.topIdx)
	}

	// 跨 Read 拆开的序列也要能拼接识别。
	k2 := &keyStream{}
	k2.feed(s, []byte{0x1b, '['})
	k2.feed(s, []byte{'1', ';', '5'})
	k2.feed(s, []byte{'A'})
	if s.topIdx != 2 {
		t.Fatalf("split seq: topIdx=%d, want 2", s.topIdx)
	}

	if got := inputText(s); got != "" {
		t.Fatalf("nav keys leaked into input: %q", got)
	}
}

// TestKeyStreamBracketedPaste 验证 TUI 括号粘贴（spec 规则 33）：粘贴
// 内 CR/LF 折叠为空格、不触发 onLine；结束标记后的回车才提交。
func TestKeyStreamBracketedPaste(t *testing.T) {
	s := &tuiState{}
	k := &keyStream{}
	var lines []string
	k.feedWith(s, []byte("\x1b[200~hello\r\nworld\x1b[201~"), func(l string) { lines = append(lines, l) })
	if len(lines) != 0 {
		t.Fatalf("paste submitted lines: %v", lines)
	}
	if got := string(s.input); got != "hello world" {
		t.Fatalf("input = %q, want %q", got, "hello world")
	}
	k.feedWith(s, []byte("\r"), func(l string) { lines = append(lines, l) })
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Fatalf("submit = %v", lines)
	}
	// 粘贴态已退出：普通回车恢复逐行提交语义。
	s.input = []rune("after")
	k.feedWith(s, []byte("\r"), func(l string) { lines = append(lines, l) })
	if len(lines) != 2 || lines[1] != "after" {
		t.Fatalf("post-paste submit = %v", lines)
	}
}
