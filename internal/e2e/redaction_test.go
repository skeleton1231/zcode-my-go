package e2e

// 工具输出密钥打码（spec 规则 73）的全栈 e2e：read 读到含密钥的文件
//——事件、wire（下一请求的 tool_result）、会话 JSONL 三处都必须只见
// [REDACTED:…]；开关关闭后原样（逃生口）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/redact"
)

// 一把无任何模式前缀的「自家」密钥（只有精确匹配能抓）与两把模式形
// 态的假密钥。
// 夹具运行时拼接：源码不落完整密钥形态（GitGuardian 扫源文本）。
var (
	ownKey     = concat2("zgoe2e-own-", "secret-key-0123456789abcdef")
	antKey     = concat2("sk-", "ant-api03-zgoe2e-abcdefghijklmnopqrs")
	openaiLike = concat2("sk-", "zgoe2e-abcdefghijklmnopqrstuv")
)

func concat2(parts ...string) string { return strings.Join(parts, "") }

func TestE2ERedactionAcrossTranscriptWireAndJSONL(t *testing.T) {
	t.Cleanup(func() { redact.Set(); redact.SetEnabled(true) })
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"keys.txt"}`}}},
		Turn{Text: "done"},
	)
	// 自家 key 注入设置（syncRedaction 喂精确匹配）。
	if _, err := h.Svc.SetAPIKey(ownKey); err != nil {
		t.Fatal(err)
	}
	h.WriteWorkspace("keys.txt", "MY_KEY="+ownKey+"\nOTHER="+antKey+"\nOPENAI="+openaiLike+"\n普通行\n")

	events := h.Ask("读一下")

	// 1) 事件面：工具结果带 [REDACTED]，不带任何裸密钥。
	sawRedacted := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "read" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "[REDACTED:secret]") &&
				strings.Contains(ev.ResultContent, "[REDACTED:anthropic-key]") &&
				strings.Contains(ev.ResultContent, "[REDACTED:openai-key]") {
				sawRedacted = true
			}
			for _, k := range []string{ownKey, antKey, openaiLike} {
				if strings.Contains(ev.ResultContent, k) {
					t.Fatalf("raw secret %q in event", k)
				}
			}
		}
	}
	if !sawRedacted {
		t.Fatal("no redacted tool-result event found")
	}

	// 2) wire 面：第二次请求（携带 tool_result 的 user 消息）不含裸密钥。
	if len(h.Script.Requests) < 2 {
		t.Fatal("expected second provider request")
	}
	for _, req := range h.Script.Requests {
		for _, m := range req.Messages {
			for _, b := range m.Blocks {
				for _, k := range []string{ownKey, antKey, openaiLike} {
					if strings.Contains(b.Text, k) {
						t.Fatalf("raw secret %q crossed the wire", k)
					}
				}
			}
		}
	}

	// 3) 持久面：会话 JSONL 文件本身无裸密钥。
	raw, err := os.ReadFile(filepath.Join(h.Svc.SessionDir(), h.SessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{ownKey, antKey, openaiLike} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("raw secret %q persisted to JSONL", k)
		}
	}
}

func TestE2ERedactionEscapeHatch(t *testing.T) {
	t.Cleanup(func() { redact.Set(); redact.SetEnabled(true) })
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"fixture.txt"}`}}},
		Turn{Text: "done"},
	)
	h.WriteWorkspace("fixture.txt", "TEST_KEY="+antKey+"\n")
	if err := h.Svc.SetRedaction(false); err != nil {
		t.Fatal(err)
	}
	events := h.Ask("原样读")
	raw := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "read" && !ev.IsError {
			if strings.Contains(ev.ResultContent, antKey) {
				raw = true
			}
		}
	}
	if !raw {
		t.Fatal("escape hatch broken: output still redacted after SetRedaction(false)")
	}
}
