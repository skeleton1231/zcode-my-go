package e2e

// 最近的子系统（自定义命令 / 每日预算 / /perms）的全栈 e2e 补全：经真实
// service + 脚本 provider 走完整路径（规则 39/35/36）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/slash"
)

// TestE2ECustomCommandSendsExpanded 验证自定义命令经真实服务展开发送：
// 工作区 .zgo/commands/run.md 的 $ARGUMENTS 被替换后进入转录。
func TestE2ECustomCommandSendsExpanded(t *testing.T) {
	h := New(t, Turn{Text: "收到"})
	if err := h.Svc.TrustWorkspace(h.Workspace, "test"); err != nil { // 信任门（spec 规则 53）：未授信不带工作区层
		t.Fatal(err)
	}
	cmdDir := filepath.Join(h.Workspace, ".zgo", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "run.md"),
		[]byte("执行任务：$ARGUMENTS"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.Svc.SetAsker(func(agent.AskRequest) agent.AskResponse {
		return agent.AskResponse{} // 自动放行（无规则积累，专注展开断言）
	})
	// 经 slash 层全栈：Send 钩子把展开文本作为消息发送进真实 Chat——
	// 转录断言覆盖「自定义命令 → 展开 → 会话转录」整条链路。
	if handled, _ := slash.Run(&slash.Env{
		Svc:       h.Svc,
		SessionID: func() string { return h.SessionID },
		Workspace: func() string { return h.Workspace },
		Out:       func(string) {},
		Send: func(text string) {
			if err := h.Svc.Chat(h.SessionID, text, func(agent.Event) {}); err != nil {
				t.Errorf("send: %v", err)
			}
		},
	}, "/run 部署到预发"); !handled {
		t.Fatal("custom command not handled")
	}
	msgs, err := h.Svc.Messages(h.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Text() == "执行任务：部署到预发" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expanded command text not in transcript: %+v", msgs)
	}
}

// TestE2EDailyBudgetNotice 验证预算阈值通知经全栈触发：预算 100，一次
// 90-token 回合后应出现 EvBudgetNotice。
func TestE2EDailyBudgetNotice(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"budgetDailyTokens":100}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wire(t, svc, []Turn{{Text: "ok", Usage: ai.Usage{InputTokens: 90}}})
	sawNotice := false
	for _, ev := range askDirect(t, svc, info.ID, "跑") {
		if ev.Kind == agent.EvBudgetNotice {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("budget notice never fired")
	}
}

// TestE2EPermsListsAlwaysRule 验证 ask-Always 积累的规则经 /perms 列出。
// mkdir 不在 bash 只读白名单（会触发权限询问），且在 PowerShell 与
// POSIX shell 上都成功退出——错误路径的 ToolResult 不影响规则积累，但
// 这里保持命令成功，让断言只覆盖 ask → GrantSession → /perms 链路。
func TestE2EPermsListsAlwaysRule(t *testing.T) {
	// 两个回合：工具回合后接纯文本回合，循环在第二次请求收尾——否则
	// Script 会重放工具回合，mkdir 第二次执行因目录已存在而报错。
	h := New(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "bash",
			Args: Marshal(map[string]string{"command": "mkdir zgo-perms-e2e"}),
		}}},
		Turn{Text: "done"})
	h.Svc.SetAsker(func(agent.AskRequest) agent.AskResponse {
		return agent.AskResponse{Always: true}
	})
	events := h.Ask("跑一次") // 触发 ask 积累
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.IsError {
			t.Fatalf("tool errored: %s", ev.ResultContent)
		}
	}
	if rules := h.Svc.SessionPermissionRules(h.SessionID); len(rules) == 0 {
		t.Fatal("always-allow rule not accumulated")
	}
	// 经真实 /perms 命令渲染：输出应列出积累的 bash 规则。
	var out []string
	if handled, _ := slash.Run(&slash.Env{
		Svc:       h.Svc,
		SessionID: func() string { return h.SessionID },
		Workspace: func() string { return h.Workspace },
		Out:       func(line string) { out = append(out, line) },
	}, "/perms"); !handled {
		t.Fatal("/perms not handled")
	}
	if joined := strings.Join(out, "\n"); !strings.Contains(joined, "bash") {
		t.Fatalf("/perms output missing accumulated rule: %q", joined)
	}
}

// askDirect 与 Harness.Ask 同语义，供手工搭的服务用。
func askDirect(t *testing.T, svc *service.AgentService, sessionID, text string) []agent.Event {
	t.Helper()
	var events []agent.Event
	if err := svc.Chat(sessionID, text, func(ev agent.Event) {
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	return events
}
