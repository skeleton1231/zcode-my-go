package tools

// gopls 三工具（spec 规则 67）：注入假 GoplsQuery 断言参数成型、jail、
// 空输出语义与截断；真 gopls 路径由 e2e 的 skip-if-absent 覆盖。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

func goplsToolSet(t *testing.T) ([]agent.Tool, *[]string) {
	t.Helper()
	var calls []string
	ws := Workspace{Root: t.TempDir()}
	tools := NewGoplsTools(ws, func(ctx context.Context, dir string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "out", nil
	})
	return tools, &calls
}

func TestGoplsPositionTools(t *testing.T) {
	ts, calls := goplsToolSet(t)
	var def, refs agent.Tool
	for _, tool := range ts {
		switch tool.Name() {
		case "definition":
			def = tool
		case "references":
			refs = tool
		}
	}
	if def == nil || refs == nil {
		t.Fatalf("tools: %v", ts)
	}
	// 参数成型：file 过 jail 成绝对路径 + 位置后缀。
	res := def.Execute(context.Background(), []byte(`{"file":"a/b.go","line":12,"column":34}`))
	if res.IsError {
		t.Fatalf("definition: %s", res.Content)
	}
	if len(*calls) == 0 || !strings.HasSuffix((*calls)[0], ":12:34") ||
		!strings.Contains((*calls)[0], "a") {
		t.Fatalf("call args: %v", *calls)
	}
	if !strings.HasPrefix((*calls)[0], "definition ") {
		t.Fatalf("subcommand: %v", *calls)
	}
	// 越界路径拒绝（path jail）。
	if res := def.Execute(context.Background(), []byte(`{"file":"../../etc/x.go","line":1,"column":1}`)); !res.IsError {
		t.Fatal("jail escape accepted")
	}
	// 非法行号拒绝。
	if res := def.Execute(context.Background(), []byte(`{"file":"a.go","line":0,"column":1}`)); !res.IsError {
		t.Fatal("line 0 accepted")
	}
	// references 同型。
	if res := refs.Execute(context.Background(), []byte(`{"file":"a.go","line":3,"column":1}`)); res.IsError {
		t.Fatalf("references: %s", res.Content)
	}
	if !strings.HasPrefix((*calls)[len(*calls)-1], "references ") {
		t.Fatalf("references args: %v", *calls)
	}
}

func TestGoplsDiagnostics(t *testing.T) {
	ts, calls := goplsToolSet(t)
	var diag agent.Tool
	for _, tool := range ts {
		if tool.Name() == "diagnostics" {
			diag = tool
		}
	}
	if diag == nil {
		t.Fatal("diagnostics tool missing")
	}
	// 空参：全工作区形态 ./...。
	if res := diag.Execute(context.Background(), []byte(`{}`)); res.IsError {
		t.Fatalf("workspace form: %s", res.Content)
	}
	if (*calls)[0] != "check ./..." {
		t.Fatalf("workspace args: %v", *calls)
	}
	// 带文件：jail 解析后的绝对路径。
	if res := diag.Execute(context.Background(), []byte(`{"file":"x/y.go"}`)); res.IsError {
		t.Fatalf("file form: %s", res.Content)
	}
	if !strings.HasPrefix((*calls)[1], "check ") || !strings.Contains((*calls)[1], "y.go") {
		t.Fatalf("file args: %v", *calls)
	}
	// 越界拒绝。
	if res := diag.Execute(context.Background(), []byte(`{"file":"../out.go"}`)); !res.IsError {
		t.Fatal("jail escape accepted")
	}
}

func TestGoplsEmptyOutputSemantics(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	empty := func(ctx context.Context, dir string, args ...string) (string, error) {
		return "  \n", nil
	}
	for _, tool := range NewGoplsTools(ws, empty) {
		args := []byte(`{"file":"a.go","line":1,"column":1}`)
		if tool.Name() == "symbol" { // spec 规则 99：查询形态不同
			args = []byte(`{"query":"Foo"}`)
		}
		res := tool.Execute(context.Background(), args)
		if res.IsError {
			t.Fatalf("%s: %s", tool.Name(), res.Content)
		}
		if res.Content == "out" || !strings.Contains(strings.ToLower(res.Content), "no ") {
			t.Fatalf("%s empty semantics: %q", tool.Name(), res.Content)
		}
	}
}

func TestGoplsOutputCapped(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	big := strings.Repeat("x", 64<<10)
	ts := NewGoplsTools(ws, func(ctx context.Context, dir string, args ...string) (string, error) {
		return big, nil
	})
	for _, tool := range ts {
		res := tool.Execute(context.Background(), []byte(`{"file":"a.go","line":1,"column":1}`))
		if len(res.Content) > goplsHead+goplsTail+512 {
			t.Fatalf("%s output = %d bytes, want capped", tool.Name(), len(res.Content))
		}
	}
}

func TestGoplsUnwired(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	for _, tool := range NewGoplsTools(ws, nil) {
		res := tool.Execute(context.Background(), []byte(`{"file":"a.go","line":1,"column":1}`))
		if !res.IsError {
			t.Fatalf("%s executed without runner", tool.Name())
		}
	}
}

// TestGoplsSymbolTool（spec 规则 99）：query 透传、空 query 拒绝、空输
// 出语义、无 location 参数不涉 jail。
func TestGoplsSymbolTool(t *testing.T) {
	set, calls := goplsToolSet(t)
	var tool agent.Tool
	for _, t2 := range set {
		if t2.Name() == "symbol" {
			tool = t2
		}
	}
	if tool == nil {
		t.Fatal("symbol tool missing from set")
	}
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"ParsePos"}`))
	if res.IsError || res.Content != "out" {
		t.Fatalf("execute = %+v", res)
	}
	if got := (*calls)[0]; got != "symbol ParsePos" {
		t.Fatalf("call = %q", got)
	}
	// 空 query 拒绝。
	if res := tool.Execute(context.Background(), json.RawMessage(`{"query":"  "}`)); !res.IsError {
		t.Fatal("empty query accepted")
	}
	// 只读能力。
	if c := tool.(interface {
		Capability() permission.Capability
	}).Capability(); !c.ReadOnly {
		t.Fatal("symbol not readonly")
	}
}
