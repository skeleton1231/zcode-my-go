package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// newTestEditor 构造非终端版编辑器（in/out 注入，restore 空）。
func newTestEditor(input string) (*editorState, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &editorState{
		in:      bufio.NewReader(strings.NewReader(input)),
		out:     out,
		restore: func() {},
	}, out
}

// TestEditorTypingSubmit 验证基础形态：字符插入、Enter 提交、连续重
// 复折叠入历史。
func TestEditorTypingSubmit(t *testing.T) {
	e, _ := newTestEditor("hello\rhello\rworld\r")
	for _, want := range []string{"hello", "hello", "world"} {
		line, ok, quit := e.readLine("zgo> ")
		if !ok || quit || line != want {
			t.Fatalf("line = %q ok=%v quit=%v, want %q", line, ok, quit, want)
		}
	}
	if len(e.history) != 2 { // hello 折叠
		t.Fatalf("history = %v", e.history)
	}
}

// TestEditorBackspaceRuneSafe 验证退格删整 rune（中文不裂）。
func TestEditorBackspaceRuneSafe(t *testing.T) {
	e, out := newTestEditor("你好\x7f\r")
	line, ok, _ := e.readLine("zgo> ")
	if !ok || line != "你" {
		t.Fatalf("line = %q", line)
	}
	if !strings.Contains(out.String(), "你") {
		t.Fatalf("render lost text: %q", out.String())
	}
}

// TestEditorHistoryRecallDraft 验证 ↑/↓ 召回与草稿恢复。
func TestEditorHistoryRecallDraft(t *testing.T) {
	e, _ := newTestEditor("one\r")
	if _, ok, _ := e.readLine("zgo> "); !ok {
		t.Fatal("first submit failed")
	}
	// 第二行：敲两个字符后 ↑（召回 one）→ ↓（恢复草稿）→ 提交。
	e2, _ := newTestEditor("dr\x1b[A\x1b[B\r")
	e2.history = e.history
	line, ok, _ := e2.readLine("zgo> ")
	if !ok || line != "dr" {
		t.Fatalf("draft not restored: %q", line)
	}
	// 纯 ↑ 召回最近一条。
	e3, _ := newTestEditor("\x1b[A\r")
	e3.history = e.history
	line, ok, _ = e3.readLine("zgo> ")
	if !ok || line != "one" {
		t.Fatalf("recall = %q", line)
	}
}

// TestEditorCtrlC 验证 Ctrl+C：空行退出信号、有内容清行。
func TestEditorCtrlC(t *testing.T) {
	e, _ := newTestEditor("\x03")
	if _, _, quit := e.readLine("zgo> "); !quit {
		t.Fatal("empty Ctrl+C must quit")
	}
	e2, _ := newTestEditor("abc\x03\r")
	line, ok, _ := e2.readLine("zgo> ")
	if !ok || line != "" { // 清行后回车提交空行
		t.Fatalf("after clear: %q ok=%v", line, ok)
	}
}

// TestEditorCursorMovement 验证 ←/→ 与中间插入、Home/End。
func TestEditorCursorMovement(t *testing.T) {
	// "ab" → ←（光标在 a|b）→ 插 "X" → "aXb" → Home → 插 "0" → "0aXb"
	// → End → 插 "!" → "0aXb!"。
	e, _ := newTestEditor("ab\x1b[DX\x1b[H0\x1b[F!\r")
	line, ok, _ := e.readLine("zgo> ")
	if !ok || line != "0aXb!" {
		t.Fatalf("line = %q", line)
	}
}
