package main

import (
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

func TestAgentPanelLines(t *testing.T) {
	if agentPanelLines(nil) != nil {
		t.Fatal("empty panel occupies lines")
	}
	views := []tools.AgentJobView{
		{ID: "ag-1", State: tools.AgentRunning, Prompt: "调查模块结构", Duration: 45 * time.Second},
		{ID: "ag-2", State: tools.AgentDone, Prompt: "审计依赖\n第二行"},
		{ID: "ag-3", State: tools.AgentFailed, Prompt: "x"},
		{ID: "ag-4", State: tools.AgentStopped, Prompt: "y"},
		{ID: "ag-5", State: tools.AgentRunning, Prompt: "z"},
	}
	lines := agentPanelLines(views)
	if len(lines) != 6 { // 头 + 4 条 + 折叠
		t.Fatalf("lines = %d %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "2 运行/5") {
		t.Fatalf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "▶ ag-1 45s 调查模块结构") {
		t.Fatalf("running = %q", lines[1])
	}
	if !strings.Contains(lines[2], "✓ ag-2 审计依赖") {
		t.Fatalf("done = %q", lines[2])
	}
	if !strings.Contains(lines[3], "✕ ag-3") || !strings.Contains(lines[4], "■ ag-4") {
		t.Fatalf("terminal marks = %q / %q", lines[3], lines[4])
	}
	if !strings.Contains(lines[5], "…+1") {
		t.Fatalf("fold = %q", lines[5])
	}
}

func TestAgentPanelBriefTruncates(t *testing.T) {
	long := strings.Repeat("长", 50)
	got := agentTaskBrief(long)
	if n := len([]rune(got)); n != 33 { // 32 + …
		t.Fatalf("brief = %d runes", n)
	}
}
