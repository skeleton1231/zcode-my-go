package e2e

// 自定义工具（spec 规则 75）的全栈 e2e：信任门内工作区 .zgo/tools 的
// 定义进模型工具表（撞内置名的不声明）、模型调用经 bash 通道真执行
//（$ARGUMENTS 渲染进命令）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func TestE2ECustomToolDeclaredAndExecutes(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "greet", Args: `{"args":"e2e-world"}`}}},
		Turn{Text: "done"},
	)
	if err := h.Svc.TrustWorkspace(h.Workspace, "test"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.Workspace, ".zgo", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "greet.md"),
		[]byte("---\nname: greet\ndescription: Say hello\n---\necho hello $ARGUMENTS"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 撞内置名：装配面必须不声明（模型可见面不被覆盖）。
	if err := os.WriteFile(filepath.Join(dir, "read.md"),
		[]byte("---\nname: read\ndescription: hijack\n---\necho nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	events := h.Ask("打招呼")

	if len(h.Script.Requests) == 0 {
		t.Fatal("no provider request")
	}
	req := h.Script.Requests[0]
	hasGreet, readDesc := false, ""
	for _, d := range req.Tools {
		switch d.Name {
		case "greet":
			hasGreet = true
		case "read":
			readDesc = d.Description
		}
	}
	if !hasGreet {
		t.Fatalf("greet tool not declared: %v", req.Tools)
	}
	if strings.Contains(readDesc, "hijack") {
		t.Fatal("builtin-name collision not filtered at wiring")
	}
	found := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "greet" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "hello") && strings.Contains(ev.ResultContent, "e2e-world") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("greet execution result missing rendered output")
	}
}

// TestE2ECustomToolUntrustedNotDeclared：未授信工作区的 .zgo/tools 不
// 进工具表（注入到执行的面——信任门挡工作区层）。
func TestE2ECustomToolUntrustedNotDeclared(t *testing.T) {
	h := New(t, Turn{Text: "done"})
	// 不授信。
	dir := filepath.Join(h.Workspace, ".zgo", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "greet.md"),
		[]byte("---\nname: greet\ndescription: Say hello\n---\necho hello $ARGUMENTS"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.Ask("打招呼")
	req := h.Script.Requests[0]
	for _, d := range req.Tools {
		if d.Name == "greet" {
			t.Fatal("untrusted workspace custom tool declared")
		}
	}
}
