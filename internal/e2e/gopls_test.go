package e2e

// gopls 集成（spec 规则 67，issue #222/#262）的全栈 e2e：真 gopls 环境
// 跑 references 符号命中与 edit 诊断回灌；gopls 缺失时整组 skip（CI
// 不预装 gopls——doctor 引导安装是设计内路径）。工具面在 gopls 缺失
// 时不声明的行为由服务层保证。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/lsp"
)

// goplsHere 报告本机 gopls 可用（e2e 真跑前置）。
func goplsHere(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH — install with: go install golang.org/x/tools/gopls@latest")
	}
}

// miniModule 建一个含同名符号文本干扰的迷你模块：Call 真函数 vs 注释
// 与字符串里的 "Call"——grep 会误报，references 不应。
func miniModule(t *testing.T) (ws, file string, line, col int) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module mini\n\ngo 1.22\n")
	write("util/util.go", "package util\n\n// Call is documented here, not a reference.\nfunc Call() int { return 1 }\n")
	write("main.go", "package main\n\nimport \"mini/util\"\n\n// See Call in the docs.\nconst note = \"Call me maybe\"\n\nfunc main() {\n\t_ = util.Call()\n}\n")
	// util.Call() 的调用在 main.go 第 9 行；Call 首列 11（tab + `_ = util.`）。
	return root, "main.go", 9, 11
}

func TestE2EGoplsReferencesSymbolPrecise(t *testing.T) {
	goplsHere(t)
	ws, file, line, col := miniModule(t)
	out, err := lsp.References(context.Background(), ws, file, line, col)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	// 被调点即 main.go:9 的调用本身（references 列用法点；声明归
	// definition 工具）。
	if !strings.Contains(out, "main.go:9") {
		t.Fatalf("references missed the call site: %q", out)
	}
	// 符号精度：注释与字符串里的 "Call" 不出现在被调点清单。
	for _, noise := range []string{"Call me maybe", "See Call in the docs", "documented here"} {
		if strings.Contains(out, noise) {
			t.Fatalf("textual noise leaked into references: %q in %q", noise, out)
		}
	}
}

func TestE2EGoplsDefinition(t *testing.T) {
	goplsHere(t)
	ws, file, line, col := miniModule(t)
	out, err := lsp.Definition(context.Background(), ws, file, line, col)
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if !strings.Contains(out, "util.go") {
		t.Fatalf("definition not at the declaration: %q", out)
	}
}

func TestE2EGoplsDiagnosticsAndEditBackflow(t *testing.T) {
	goplsHere(t)
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "edit", Args: `{"path":"main.go","old_string":"func main() {","new_string":"func broken( {"}`}}},
		Turn{Text: "done"},
	)
	// 工作区里先放同一个迷你模块（edit 打它）。
	root := h.Workspace
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module mini\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(
		"package main\n\nfunc main() {\n\tprintln(1)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	events := h.Ask("改坏它")
	found := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "edit" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "[gopls]") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("edit result lacks the [gopls] diagnostics tail")
	}
}
