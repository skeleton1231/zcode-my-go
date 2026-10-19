package tools

// 自定义工具（spec 规则 75）：.zgo/tools 定义的命令模板包装成模型可
// 调用的工具。执行完全复用 Bash 通道（超时/输出上限/权限门/path
// jail/远程同构/cwd 哨兵）——权限按渲染后的最终命令判定（只读探测
// 看到的是真命令，不是模板）。

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/tooldefs"
)

// customArgsPlaceholder 是模板占位符（与自定义命令同字面）。
const customArgsPlaceholder = "$ARGUMENTS"

// NewCustomTools 把定义集包装为工具（每个 def 一个）。内置名过滤在
// 调用方（service 侧已装配完整工具面，撞名判定零成本）。
func NewCustomTools(defs []tooldefs.Def, ws Workspace, jobs *Jobs) []agent.Tool {
	var out []agent.Tool
	for _, def := range defs {
		run := NewBash(ws, jobs)
		out = append(out, &customTool{def: def, run: run})
	}
	return out
}

type customTool struct {
	def tooldefs.Def
	run *Bash
}

func (t *customTool) Name() string { return t.def.Name }

func (t *customTool) Description() string {
	return t.def.Description + " (custom tool: your args fill $ARGUMENTS " +
		"in a fixed command template; the command runs in the workspace shell)"
}

func (t *customTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"args": prop("string", "arguments substituted into the command template (may be empty)"),
	}, "args")
}

// render 把用户参数填进模板（占位符缺省追加在尾部——免占位符的固定
// 命令 + 可选参数两种形态都成立）。
func (t *customTool) render(args string) string {
	if strings.Contains(t.def.Command, customArgsPlaceholder) {
		return strings.ReplaceAll(t.def.Command, customArgsPlaceholder, args)
	}
	if strings.TrimSpace(args) == "" {
		return t.def.Command
	}
	return t.def.Command + " " + args
}

func (t *customTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Args string `json:"args"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	raw, err := json.Marshal(map[string]any{"command": t.render(a.Args)})
	if err != nil {
		return errResult("%v", err)
	}
	return t.run.Execute(ctx, raw)
}

// Capability 对齐 bash：shell 命令风险面。
func (t *customTool) Capability() permission.Capability { return t.run.Capability() }

// CapabilityForArgs 按渲染后的最终命令判定（只读探测看真命令）。
func (t *customTool) CapabilityForArgs(args string) permission.Capability {
	var a struct {
		Args string `json:"args"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return t.Capability()
	}
	raw, err := json.Marshal(map[string]any{"command": t.render(a.Args)})
	if err != nil {
		return t.Capability()
	}
	return t.run.CapabilityForArgs(string(raw))
}
