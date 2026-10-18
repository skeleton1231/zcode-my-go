package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
)

// skillMaxBytes 是一次技能加载的输出上限（对齐 skills.DefaultMaxSkillBytes；
// 超限截断并标注——工具输出必须有界）。
const skillMaxBytes = skills.DefaultMaxSkillBytes

// skillListCap 是 not-found 自纠提示里可用名清单的封顶（行数）。
const skillListCap = 30

// skillTool 按名加载技能正文（ZCode 的 Skill 工具）：发现走同一套根
// （工作区 .zgo/skills + 全局 ~/.zgo/skills，SSH 同享），正文即指令。
type skillTool struct {
	roots func() []skills.Root // 每次执行时取（远程拨号由 service 缓存）
}

// NewSkillTool 构建技能工具；roots 为空返回 nil（无根不可用）。
func NewSkillTool(roots func() []skills.Root) agent.Tool {
	return &skillTool{roots: roots}
}

// Name implements agent.Tool.
func (t *skillTool) Name() string { return "skill" }

// Description implements agent.Tool.
func (t *skillTool) Description() string {
	return "Load a skill by exact name from the available-skills list in the " +
		"system prompt. The result is the skill's instructions; files it " +
		"references resolve relative to the reported base directory. Do not " +
		"guess names."
}

// Parameters implements agent.Tool.
func (t *skillTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"skill": prop("string", "the skill's name from the available-skills list"),
		"args":  prop("string", "optional arguments the skill should act on"),
	}, "skill")
}

// Execute implements agent.Tool.
func (t *skillTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Skill string `json:"skill"`
		Args  string `json:"args"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	name := strings.TrimSpace(a.Skill)
	if name == "" {
		return errResult("skill name is required")
	}
	rs := t.roots()
	out := skills.Discover(rs)
	s, ok := skills.Find(out, name)
	if !ok {
		return errResult("skill %q not found; available skills:\n%s",
			name, availableSkills(out))
	}
	root, ok := skills.RootOf(rs, s)
	if !ok {
		return errResult("skill root for %s vanished", s.Path)
	}
	content, base, truncated, err := skills.LoadSkill(root, s, skillMaxBytes)
	if err != nil {
		return errResult("loading skill %s: %v", name, err)
	}
	var b strings.Builder
	b.WriteString(content)
	if strings.TrimSpace(a.Args) != "" {
		b.WriteString("\n\nArguments:\n" + strings.TrimSpace(a.Args))
	}
	b.WriteString("\n\n(skill base directory: " + base + ")")
	if truncated {
		b.WriteString("\n[skill truncated at " + strconv.Itoa(skillMaxBytes) + " bytes]")
	}
	return agent.ToolResult{Content: b.String()}
}

// Capability implements agent.Capabler：技能是受控根内的只读指令包。
func (t *skillTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// availableSkills 渲染 not-found 时的可用名清单（发现结果已按名排序）。
func availableSkills(out skills.Outcome) string {
	if len(out.Skills) == 0 {
		return "(none discovered — add <workspace>/.zgo/skills/<name>/SKILL.md)"
	}
	names := make([]string, 0, len(out.Skills))
	for _, s := range out.Skills {
		names = append(names, s.Name)
	}
	if len(names) > skillListCap {
		names = names[:skillListCap]
	}
	return "- " + strings.Join(names, "\n- ")
}
