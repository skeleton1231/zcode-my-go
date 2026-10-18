package e2e

// 自定义子代理定义（spec 规则 51，issue #225）的全栈 e2e：定义文件经真
// 实 service 发现进系统提示清单；task 带 agent 名走定义 persona 与工具
// 白名单；未知名报错列可用项自纠。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// reviewerMD 是标准定义文件：白名单 read+grep，persona 正文。
const reviewerMD = "---\nname: reviewer\ndescription: Reviews code for defects\ntools: read, grep\n---\nYou are a meticulous code reviewer. Read the change, list defects by severity, cite file paths."

func plantAgentDef(t *testing.T, ws, file, content string) {
	t.Helper()
	dir := filepath.Join(ws, ".zgo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestE2ECustomAgentPersona 本地全栈：清单进主系统提示；子代理请求的
// 系统提示即定义 persona（默认 SubtaskSystem 被替换），工具表收窄为白
// 名单；结论经 task 结果回主对话。
func TestE2ECustomAgentPersona(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "task", Args: `{"prompt":"审查这次改动","agent":"reviewer"}`}}},
		Turn{Text: "审查结论：无阻塞问题。"}, // 子代理的回答回合
		Turn{Text: "收到"}, // 主对话收尾
	)
	plantAgentDef(t, h.Workspace, "reviewer.md", reviewerMD)

	events := h.Ask("用 reviewer 审查")

	if len(h.Script.Requests) < 3 {
		t.Fatalf("expected 3 requests, got %d", len(h.Script.Requests))
	}
	main := h.Script.Requests[0]
	if !strings.Contains(main.System, "- reviewer: Reviews code for defects") {
		t.Fatalf("agents roster missing from main system prompt:\n%s", main.System)
	}

	sub := h.Script.Requests[1] // 子代理回合
	if !strings.Contains(sub.System, "meticulous code reviewer") {
		t.Fatalf("subagent system is not the defined persona:\n%s", sub.System)
	}
	if strings.Contains(sub.System, "research subagent") {
		t.Fatalf("default SubtaskSystem leaked over the persona:\n%s", sub.System)
	}
	names := map[string]bool{}
	for _, d := range sub.Tools {
		names[d.Name] = true
	}
	if !names["read"] || !names["grep"] {
		t.Fatalf("whitelisted tools missing from subagent table: %v", sub.Tools)
	}
	if names["edit"] || names["task"] || names["bash"] {
		t.Fatalf("subagent toolset wider than whitelist: %v", sub.Tools)
	}

	collected := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "task" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "审查结论：无阻塞问题。") {
				collected = true
			}
		}
	}
	if !collected {
		t.Fatal("subagent answer never surfaced in task result")
	}
}

// TestE2EUnknownAgentListsAvailable 未命中定义：报错列出可用项供模型自
// 纠（而不是静默退回默认子代理）。
func TestE2EUnknownAgentListsAvailable(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "task", Args: `{"prompt":"x","agent":"ghost"}`}}},
		Turn{Text: "改用 reviewer 重试"},
	)
	plantAgentDef(t, h.Workspace, "reviewer.md", reviewerMD)

	events := h.Ask("去查")
	errored := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "task" && ev.IsError {
			if strings.Contains(ev.ResultContent, "ghost") && strings.Contains(ev.ResultContent, "reviewer") {
				errored = true
			}
		}
	}
	if !errored {
		t.Fatal("unknown agent did not error with the available list")
	}
}
