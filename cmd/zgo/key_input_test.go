package main

import (
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
