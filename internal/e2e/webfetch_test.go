package e2e

// webfetch（spec 规则 45）的全栈 e2e：工具声明进表、SSRF 防线经真实
// 权限与工具执行链路生效（脚本 provider 不出网——用被拒绝的私网目标
// 驱动真实工具走完整路径）。

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestE2EWebFetchDeclaredAndGuarded 工具表含 webfetch；对私网 IP 的调
// 用经真实执行被 egress 守卫拒绝（错误结果回模型自纠）。
func TestE2EWebFetchDeclaredAndGuarded(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "webfetch",
			Args: `{"url":"https://192.168.1.1/admin"}`}}},
		Turn{Text: "done"},
	)
	events := h.Ask("抓一下")

	if len(h.Script.Requests) == 0 {
		t.Fatal("no provider request")
	}
	hasTool := false
	for _, d := range h.Script.Requests[0].Tools {
		if d.Name == "webfetch" {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("webfetch not declared: %v", h.Script.Requests[0].Tools)
	}

	blocked := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "webfetch" {
			if !ev.IsError {
				t.Fatalf("private IP fetch not blocked: %s", ev.ResultContent)
			}
			if strings.Contains(ev.ResultContent, "拒绝") {
				blocked = true
			}
		}
	}
	if !blocked {
		t.Fatal("egress block message never surfaced")
	}
	// 错误结果回到模型手里（第二请求含拒绝信息）。
	if len(h.Script.Requests) < 2 {
		t.Fatal("blocked result never returned to the model")
	}
	found := false
	for _, m := range h.Script.Requests[1].Messages {
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockToolResult && strings.Contains(b.Text, "拒绝") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("block message missing from second request")
	}
}
