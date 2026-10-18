package tools

// gopls 三工具（LSP 路线 A v1，spec 规则 67，issue #222/#262）：符号
// 语义的 definition/references 与即时 diagnostics——grep 文本匹配给不
// 出的精度。文件参数一律过 path jail；输出照家族截断。调用面注入
//（测试用假 gopls；真实现接 internal/lsp 的子进程调用）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// GoplsQuery 是 gopls 子命令调用面：dir 是工作区根，args 形如
// ("definition", "file.go:12:34")。返回清洗后的文本输出。
type GoplsQuery func(ctx context.Context, dir string, args ...string) (string, error)

// ProbeDiagnostics 是 edit/write 成功回执的即时诊断回灌点（spec 规则
// 67）：服务层在 gopls 可用时接 internal/lsp.Probe，nil 或空串即无尾
// 部。var 形态（同 WebFetcher 约定）——edit/write 构造不感知 gopls。
var ProbeDiagnostics func(ctx context.Context, root, absPath string) string

// diagTail 渲染回灌尾部（仅 gopls 有话说时）；上限 4KB 只截尾。
func diagTail(ctx context.Context, root, abs string) string {
	if ProbeDiagnostics == nil || root == "" {
		return ""
	}
	if text := strings.TrimSpace(ProbeDiagnostics(ctx, root, abs)); text != "" {
		return "\n\n[gopls]\n" + truncateBytes([]byte(text), 4<<10, 0)
	}
	return ""
}

// goplsHead/goplsTail 是 gopls 输出的截断上限（对齐 task 家族）。
const goplsHead, goplsTail = 8 << 10, 4 << 10

// Gopls 携带三工具的共享装配。
type Gopls struct {
	ws  Workspace
	run GoplsQuery
}

// NewGoplsTools 返回 definition/references/diagnostics 三工具。run 为
// nil 时工具执行报未接线（防御——服务层缺 gopls 时不装配本组）。
func NewGoplsTools(ws Workspace, run GoplsQuery) []agent.Tool {
	g := &Gopls{ws: ws, run: run}
	return []agent.Tool{&goplsPositionTool{g: g, mode: "definition"},
		&goplsPositionTool{g: g, mode: "references"},
		&goplsDiagnosticsTool{g: g}}
}

// goplsPositionTool 是 definition/references 双形态（同为 file:line:col
// 定位，仅子命令与文案不同）。
type goplsPositionTool struct {
	g    *Gopls
	mode string // "definition" | "references"
}

func (t *goplsPositionTool) Name() string { return t.mode }

func (t *goplsPositionTool) Description() string {
	if t.mode == "definition" {
		return "Find where the symbol at file:line:column is declared. " +
			"Symbol-precise (unlike grep): same-named methods, comments " +
			"and strings do not match. Requires gopls."
	}
	return "List every call site of the symbol at file:line:column. " +
		"Symbol-precise (unlike grep): same-named methods, comments and " +
		"strings do not produce false hits. Requires gopls."
}

func (t *goplsPositionTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"file":   prop("string", "Workspace-relative file path"),
		"line":   prop("integer", "1-based line of the symbol"),
		"column": prop("integer", "1-based column of the symbol"),
	}, "file", "line", "column")
}

func (t *goplsPositionTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.Line < 1 || a.Column < 1 {
		return errResult("line and column are 1-based")
	}
	if t.g.run == nil {
		return errResult("gopls tools not wired")
	}
	abs, err := t.g.ws.Resolve(a.File)
	if err != nil {
		return errResult("%v", err)
	}
	out, err := t.g.run(ctx, t.g.ws.Root, t.mode, fmt.Sprintf("%s:%d:%d", abs, a.Line, a.Column))
	return goplsResult(out, err, t.mode)
}

// goplsDiagnosticsTool 是即时诊断（空参全工作区）。
type goplsDiagnosticsTool struct{ g *Gopls }

func (t *goplsDiagnosticsTool) Name() string { return "diagnostics" }

func (t *goplsDiagnosticsTool) Description() string {
	return "Compiler/linter diagnostics from gopls for the whole workspace " +
		"or one file — fresher than waiting for the next bash build. " +
		"Empty output means no findings. Requires gopls."
}

func (t *goplsDiagnosticsTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"file": prop("string", "Optional workspace-relative file; omit for the whole workspace"),
	})
}

func (t *goplsDiagnosticsTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		File string `json:"file"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if t.g.run == nil {
		return errResult("gopls tools not wired")
	}
	target := "./..."
	if a.File != "" {
		abs, err := t.g.ws.Resolve(a.File)
		if err != nil {
			return errResult("%v", err)
		}
		target = abs
	}
	out, err := t.g.run(ctx, t.g.ws.Root, "check", target)
	return goplsResult(out, err, "diagnostics")
}

// goplsResult 统一成型：空输出按查询语义呈现（definition 无命中是常
// 态，references 空即无被调点，diagnostics 空即无错）；错误保可读。
func goplsResult(out string, err error, mode string) agent.ToolResult {
	if err != nil {
		return errResult("gopls %s failed: %v", mode, err)
	}
	if strings.TrimSpace(out) == "" {
		switch mode {
		case "definition":
			return agent.ToolResult{Content: "no definition found at that position"}
		case "references":
			return agent.ToolResult{Content: "no references found"}
		default:
			return agent.ToolResult{Content: "no diagnostics"}
		}
	}
	return agent.ToolResult{Content: truncateBytes([]byte(out), goplsHead, goplsTail)}
}

// Capability implements agent.Capabler: 符号查询只读低风险。
func (t *goplsPositionTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// Capability implements agent.Capabler.
func (t *goplsDiagnosticsTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}
