package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

func TestTuiEventColors(t *testing.T) {
	defer func(enabled bool) { colorEnabled = enabled }(colorEnabled)

	colorEnabled = true
	s := &tuiState{width: 120}
	s.applyEvent(agent.Event{Kind: agent.EvMessageStart}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvTextDelta, Text: "hi"}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvToolExecutionStart, ToolName: "read", Args: "{}"}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvToolExecutionEnd, IsError: true, ResultContent: "x"}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvMessageEnd, StopReason: "error", ErrorMessage: "boom"}, new(string))
	joined := strings.Join(s.lines, "\n")
	for _, want := range []string{"\x1b[1m", "\x1b[90m", "\x1b[31m"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}

	// NO_COLOR：禁色后无转义。
	colorEnabled = false
	s2 := &tuiState{width: 120}
	s2.applyEvent(agent.Event{Kind: agent.EvMessageStart}, new(string))
	if strings.Contains(s2.lines[0], "\x1b") {
		t.Fatalf("escape leaked with colors off: %q", s2.lines[0])
	}
}

func TestAnsiHelper(t *testing.T) {
	defer func(e bool) { colorEnabled = e; os.Unsetenv("NO_COLOR") }(colorEnabled)
	os.Setenv("NO_COLOR", "1")
	colorEnabled = os.Getenv("NO_COLOR") == ""
	if colorEnabled {
		t.Fatal("NO_COLOR not honored")
	}
	if got := ansi("31", "x"); got != "x" {
		t.Fatalf("plain = %q", got)
	}
	colorEnabled = true
	if got := ansi("31", "x"); got != "\x1b[31mx\x1b[0m" {
		t.Fatalf("colored = %q", got)
	}
}

// TestTuiPlanLines 验证任务面板块：头行计数、状态标记、超限折叠。
func TestTuiPlanLines(t *testing.T) {
	plan := []tools.TodoTask{
		{Content: "a", Status: tools.TodoDone},
		{Content: "b", Status: tools.TodoInProgress},
		{Content: "c"},
	}
	got := planLines(plan, planMaxRows)
	if len(got) != 4 { // 头 + 3 任务
		t.Fatalf("len = %d, want 4: %q", len(got), got)
	}
	if !strings.Contains(got[0], "计划 · 1/3") {
		t.Errorf("header = %q", got[0])
	}
	for i, want := range []string{"✓ a", "▶ b", "□ c"} {
		if !strings.Contains(got[i+1], want) {
			t.Errorf("row %d = %q, want %q", i+1, got[i+1], want)
		}
	}

	// 超限折叠：8 条任务 → 6 条 + …+2。
	big := make([]tools.TodoTask, 8)
	overflow := planLines(big, planMaxRows)
	if last := overflow[len(overflow)-1]; !strings.Contains(last, "…+2") {
		t.Errorf("fold = %q, want …+2", last)
	}
	if planLines(nil, planMaxRows) != nil {
		t.Error("empty plan should hide the block")
	}
}

// TestTuiJumpTurnNavigation 验证回合跳转语义与桌面一致：从尾随首跳到
// 最后一轮、边界不动、向下越过最新恢复尾随。
func TestTuiJumpTurnNavigation(t *testing.T) {
	s := &tuiState{width: 40, height: 12, topIdx: -1, turnCur: -1}
	s.pushTurn("q1")            // 行 0
	s.push("r1")
	s.push("r2")
	s.pushTurn("q2")            // 行 3
	s.push("r3")
	s.push("r4")
	s.pushTurn("q3")            // 行 6
	for i := 0; i < 24; i++ {   // 填到 30 行，避免窗口夹紧吞掉断言差异
		s.push(fmt.Sprintf("filler %d", i))
	}
	if len(s.turns) != 3 || s.turns[2] != 6 {
		t.Fatalf("turns = %v", s.turns)
	}

	s.jumpTurn(-1)
	if s.topIdx != 6 || s.turnCur != 2 {
		t.Fatalf("first jump: topIdx=%d turnCur=%d, want 6/2", s.topIdx, s.turnCur)
	}
	s.jumpTurn(-1)
	if s.topIdx != 3 {
		t.Fatalf("second jump: topIdx=%d, want 3", s.topIdx)
	}
	s.jumpTurn(-1)
	if s.topIdx != 0 {
		t.Fatalf("third jump: topIdx=%d, want 0", s.topIdx)
	}
	s.jumpTurn(-1) // 最早回合：不动
	if s.topIdx != 0 {
		t.Fatalf("boundary: topIdx=%d, want 0", s.topIdx)
	}
	s.jumpTurn(1)
	s.jumpTurn(1)
	if s.topIdx != 6 {
		t.Fatalf("back down: topIdx=%d, want 6", s.topIdx)
	}
	s.jumpTurn(1) // 越过最新 → 恢复尾随
	if s.topIdx != -1 || s.turnCur != -1 {
		t.Fatalf("past latest: topIdx=%d turnCur=%d, want -1/-1", s.topIdx, s.turnCur)
	}
}

// TestTuiPageScrollClamp 验证半屏滚动的夹紧与到底恢复尾随。
func TestTuiPageScrollClamp(t *testing.T) {
	s := &tuiState{width: 40, height: 12, topIdx: -1, turnCur: -1} // body=10
	for i := 0; i < 100; i++ {
		s.push(fmt.Sprintf("line %d", i))
	}
	s.pageScroll(-1) // 尾随窗口顶 90 → 85
	if s.topIdx != 85 {
		t.Fatalf("pageUp: topIdx=%d, want 85", s.topIdx)
	}
	s.pageScroll(-1) // → 80
	if s.topIdx != 80 {
		t.Fatalf("pageUp 2: topIdx=%d, want 80", s.topIdx)
	}
	s.pageScroll(1) // → 85
	if s.topIdx != 85 {
		t.Fatalf("pageDown: topIdx=%d, want 85", s.topIdx)
	}
	for i := 0; i < 4; i++ {
		s.pageScroll(1) // 90 …
	}
	if s.topIdx != -1 { // 滚到底 → 尾随
		t.Fatalf("bottom: topIdx=%d, want -1", s.topIdx)
	}
	s.topIdx = 2
	s.pageScroll(-1) // 越顶夹紧到 0
	if s.topIdx != 0 {
		t.Fatalf("clamp top: topIdx=%d, want 0", s.topIdx)
	}
}

// TestTuiDrawPlanAndViewport 验证绘制：计划块置顶且压缩转录窗口、视口
// 按绝对下标取窗、尾随贴底。
func TestTuiDrawPlanAndViewport(t *testing.T) {
	defer func(enabled bool) { colorEnabled = enabled }(colorEnabled)
	colorEnabled = false

	s := &tuiState{width: 60, height: 12, topIdx: -1, turnCur: -1}
	s.plan = []tools.TodoTask{
		{Content: "done-task", Status: tools.TodoDone},
		{Content: "wip-task", Status: tools.TodoInProgress},
	}
	for i := 0; i < 30; i++ {
		s.push(fmt.Sprintf("line %d", i))
	}
	var buf bytes.Buffer
	s.draw(&buf)
	out := buf.String()
	for _, want := range []string{"计划 · 1/2", "✓ done-task", "▶ wip-task", "line 29", "line 23"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in draw output", want)
		}
	}
	if strings.Contains(out, "line 22") { // 计划块占 3 行，body=7 → 最早可见 line 23
		t.Error("plan block should shrink the transcript window")
	}

	// 视口模式：topIdx=6 → 可见 line 6..12。
	s.topIdx = 6
	buf.Reset()
	s.draw(&buf)
	out = buf.String()
	for _, want := range []string{"line 6", "line 12"} {
		if !strings.Contains(out, want) {
			t.Errorf("viewport: missing %q", want)
		}
	}
	if strings.Contains(out, "line 5") || strings.Contains(out, "line 13") {
		t.Error("viewport window not respected")
	}
}

// TestTuiTodoEventRefreshesPlan 验证 todo 工具事件触发计划快照刷新。
func TestTuiTodoEventRefreshesPlan(t *testing.T) {
	s := &tuiState{width: 40}
	s.onPlan = func() []tools.TodoTask {
		return []tools.TodoTask{{Content: "t", Status: tools.TodoDone}}
	}
	s.applyEvent(agent.Event{Kind: agent.EvToolExecutionEnd, ToolName: "todo"}, new(string))
	if len(s.plan) != 1 || s.plan[0].Content != "t" {
		t.Fatalf("plan = %+v", s.plan)
	}

	// 其他工具不触发；nil 回调不崩。
	s2 := &tuiState{width: 40}
	s2.applyEvent(agent.Event{Kind: agent.EvToolExecutionEnd, ToolName: "read"}, new(string))
	if s2.plan != nil {
		t.Fatal("read should not refresh plan")
	}
}

// TestReplayHistory 验证恢复会话的转录回放：用户回合登记导航锚点、助
// 手文本常规样式、空文本消息跳过（规则 22）。
func TestReplayHistory(t *testing.T) {
	defer func(enabled bool) { colorEnabled = enabled }(colorEnabled)
	colorEnabled = false

	s := &tuiState{width: 80, topIdx: -1, turnCur: -1}
	msgs := []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "第一个问题"}}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "第一个回答"}}},
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: ""}}}, // 空文本跳过
		{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "第二个回答"}}},
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "第二个问题"}}},
	}
	if n := replayHistory(s, msgs); n != 2 {
		t.Fatalf("turns = %d, want 2", n)
	}
	if len(s.turns) != 2 {
		t.Fatalf("anchors = %v, want 2", s.turns)
	}
	joined := strings.Join(s.lines, "\n")
	for _, want := range []string{"你 > 第一个问题", "zgo > 第一个回答", "zgo > 第二个回答", "你 > 第二个问题"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in replay:\n%s", want, joined)
		}
	}
	// 回放后的回合可导航：首跳到最后一轮（第二个问题所在行）。
	s.jumpTurn(-1)
	if s.turnCur != 1 {
		t.Fatalf("turnCur = %d, want 1", s.turnCur)
	}
}
