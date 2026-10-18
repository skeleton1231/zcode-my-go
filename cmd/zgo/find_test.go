package main

import (
	"strings"
	"testing"
)

// newFindState 搭一个带样例行与宽度的 TUI 状态。
func newFindState(lines ...string) *tuiState {
	s := &tuiState{redraw: make(chan struct{}, 1), topIdx: -1, turnCur: -1, width: 80, height: 24}
	for _, ln := range lines {
		s.lines = append(s.lines, ln)
	}
	return s
}

func TestFindStartHitsAndJump(t *testing.T) {
	s := newFindState("第一行", "包含目标词的行", "第三行", "又有目标词", "第五行")
	out := s.findStart("目标词")
	if !strings.Contains(out, "2 处") {
		t.Fatalf("out = %q", out)
	}
	if len(s.findHits) != 2 || s.findHits[0] != 1 || s.findHits[1] != 3 {
		t.Fatalf("hits = %v", s.findHits)
	}
	if s.findCur != 1 || s.topIdx != 3 {
		t.Fatalf("cur=%d top=%d", s.findCur, s.topIdx)
	}
	// 大小写不敏感。
	if out := s.findStart("目标词"); !strings.Contains(out, "2 处") {
		t.Fatalf("case-insensitive failed: %q", out)
	}
	if out := s.findStart("不存在的词"); !strings.Contains(out, "未找到") {
		t.Fatalf("miss = %q", out)
	}
}

func TestFindMoveWraps(t *testing.T) {
	s := newFindState("a目标", "b", "c目标")
	s.findStart("目标")
	s.findMove(-1) // 1 → 0
	if s.findCur != 0 || s.topIdx != 0 {
		t.Fatalf("prev: cur=%d top=%d", s.findCur, s.topIdx)
	}
	s.findMove(-1) // 0 → 环绕到 1
	if s.findCur != 1 {
		t.Fatalf("wrap: cur=%d", s.findCur)
	}
}

func TestFindClearRestores(t *testing.T) {
	s := newFindState("目标行")
	s.findStart("目标")
	if out := s.findClear(); !strings.Contains(out, "退出") {
		t.Fatalf("out = %q", out)
	}
	if s.findQ != "" || s.findHits != nil {
		t.Fatalf("state = %+v", s)
	}
}

// TestFindKeysViaKeyStream 键路径：搜索激活时空输入区 n/N 跳转、孤立
// Esc 先退搜索；输入区非空时 n 是普通字符。
func TestFindKeysViaKeyStream(t *testing.T) {
	s := newFindState("x目标a", "y", "z目标b")
	s.width, s.height = 80, 24
	s.findStart("目标")
	cur := s.findCur // 1（最近命中）
	k := &keyStream{}
	k.feedWith(s, []byte{'n'}, nil)
	if s.findCur == cur {
		t.Fatal("n did not move")
	}
	// 输入区有草稿：n 入输入区（不误触）。
	s.input = []rune("看")
	k2 := &keyStream{}
	k2.feedWith(s, []byte{'n'}, nil)
	if string(s.input) != "看n" {
		t.Fatalf("input = %q", string(s.input))
	}
	s.input = s.input[:0]
	// 孤立 Esc：退搜索而非中止。
	k3 := &keyStream{}
	aborted := false
	s.abort = func() { aborted = true }
	k3.feedWith(s, []byte{0x1b}, nil)
	if s.findQ != "" {
		t.Fatal("esc did not clear find")
	}
	if aborted {
		t.Fatal("esc aborted instead of exiting find")
	}
}
