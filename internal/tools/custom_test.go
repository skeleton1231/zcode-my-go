package tools

// 自定义工具（spec 规则 75）的包装层测试：模板渲染（占位/追加/空
// 参）、执行走 bash 通道（真命令）、权限按渲染后命令判定（只读探测
// 看渲染出的真命令）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/tooldefs"
)

func ctDef(name, tpl string) tooldefs.Def {
	return tooldefs.Def{Name: name, Description: name + " tool", Command: tpl}
}

func ctExec(t *testing.T, c agent.Tool, args string) agent.ToolResult {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"args": args})
	if err != nil {
		t.Fatal(err)
	}
	return c.Execute(context.Background(), raw)
}

func TestCustomToolRenderAndExecute(t *testing.T) {
	ws := testWorkspace(t)
	tools := NewCustomTools([]tooldefs.Def{
		ctDef("greet", "echo hello $ARGUMENTS"),
		ctDef("fixed", "echo fixed-output"),
	}, ws, NewJobs())
	if len(tools) != 2 {
		t.Fatalf("tools = %d", len(tools))
	}

	// 占位替换（PowerShell 的 echo 按参数分行输出——两个词都在即证
	// 明 $ARGUMENTS 进了命令行）。
	r := ctExec(t, tools[0], "world")
	if r.IsError || !strings.Contains(r.Content, "hello") || !strings.Contains(r.Content, "world") {
		t.Fatalf("placeholder render: %+v", r)
	}
	// 无占位：空参=固定命令。
	r = ctExec(t, tools[1], "")
	if r.IsError || !strings.Contains(r.Content, "fixed-output") {
		t.Fatalf("fixed no-args: %+v", r)
	}
	// 元信息面。
	if tools[0].Name() != "greet" || !strings.Contains(tools[0].Description(), "greet tool") {
		t.Fatalf("meta: %q %q", tools[0].Name(), tools[0].Description())
	}
	if _, ok := tools[0].Parameters()["properties"]; !ok {
		t.Fatalf("parameters shape: %v", tools[0].Parameters())
	}
}

func TestCustomToolCapabilityUsesRenderedCommand(t *testing.T) {
	ws := testWorkspace(t)
	// 模板字面无 "ls"，渲染后是只读命令——只读探测看的是渲染结果。
	ct := NewCustomTools([]tooldefs.Def{ctDef("look", "ls $ARGUMENTS")}, ws, NewJobs())[0].(*customTool)
	raw := mustJSON(t, map[string]string{"args": "."})
	if !ct.CapabilityForArgs(string(raw)).ReadOnly {
		t.Fatal("rendered ls should be readonly")
	}
	// 写命令模板：渲染后非只读（Risk 语义交由 bash 策略）。
	ct2 := NewCustomTools([]tooldefs.Def{ctDef("mut", "rm $ARGUMENTS")}, ws, NewJobs())[0].(*customTool)
	raw2 := mustJSON(t, map[string]string{"args": "x.txt"})
	if ct2.CapabilityForArgs(string(raw2)).ReadOnly {
		t.Fatal("rendered rm must not be readonly")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
