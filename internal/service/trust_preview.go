package service

// 信任前预览（spec 规则 101）：授信前先看清「这个工作区会拿到什么」
// ——信任门（规则 53）拦截的全部注入面一次性列出：.zgorules.json 的
// 权限规则（allow 是提权面）、自定义工具/命令/技能/子代理定义。纯读
// 不落任何状态；克隆来的仓库先 preview 再决定 grant。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/agents"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
)

// TrustReport 是一个工作区的注入面清单。
type TrustReport struct {
	Workspace string
	// Trusted 是当前授信态（已授信时 preview 提示「已生效」）。
	Trusted bool
	// AllowRules 按工具计数的 allow 规则（"*" 键 = 全工具放行——最
	// 大提权面，preview 单独点名）；AskRules/DenyRules 是计数。
	AllowRules map[string]int
	AskRules   int
	DenyRules  int
	// RulesBroken 报告规则文件解析失败（预览诚实——坏文件授信后也
	// 会被忽略，但用户该知道它存在）。
	RulesBroken bool
	Tools       []string // .zgo/tools 自定义工具
	Commands    []string // .zgo/commands
	Skills      []string // .zgo/skills（工作区层）
	Agents      []string // .zgo/agents
}

// TrustPreview 汇总工作区的信任注入面（纯读；workspace 为本地路径）。
func (s *AgentService) TrustPreview(workspace string) (TrustReport, error) {
	rep := TrustReport{
		Workspace:  workspace,
		Trusted:    s.workspaceTrusted(workspace),
		AllowRules: map[string]int{},
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return rep, err
	}
	if !info.IsDir() {
		return rep, fmt.Errorf("%s 不是目录", workspace)
	}

	// 权限规则：与 loadProjectRules 同源，但预览要看坏文件。
	if data, err := os.ReadFile(filepath.Join(workspace, rulesFile)); err == nil {
		if len(data) > rulesCapBytes {
			rep.RulesBroken = true
		} else if rs, perr := permission.ParseRuleset(data); perr == nil {
			for _, r := range rs.Rules {
				switch r.Behavior {
				case permission.Allow:
					rep.AllowRules[r.Tool]++
				case permission.Ask:
					rep.AskRules++
				case permission.Deny:
					rep.DenyRules++
				}
			}
		} else {
			rep.RulesBroken = true
		}
	}

	// 其余四层：直接按目录读文件名（预览不需要完整解析——名字清单
	// 已是决策所需；同名冲突由各自的层叠规则处理）。
	rep.Tools = mdNames(filepath.Join(workspace, ".zgo", "tools"))
	rep.Commands = mdNames(filepath.Join(workspace, customCommandsDir))
	rep.Skills = skillNames(filepath.Join(workspace, ".zgo", "skills"))
	rep.Agents = agentNames(filepath.Join(workspace, ".zgo", "agents"))
	return rep, nil
}

// mdNames 列目录下 .md 文件名（去扩展名，排序）。
func mdNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) == ".md" {
			out = append(out, name[:len(name)-3])
		}
	}
	sort.Strings(out)
	return out
}

// skillNames 借 skills 包的发现面（SKILL.md 形态是子目录）。
func skillNames(dir string) []string {
	outcome := skills.Discover([]skills.Root{{Base: dir, FS: skills.LocalFS{}}})
	var out []string
	for _, s := range outcome.Skills {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

// agentNames 借 agents 包的发现面。
func agentNames(dir string) []string {
	outcome := agents.Discover([]agents.Root{{Base: dir, FS: agents.LocalFS{}}})
	var out []string
	for _, a := range outcome.Defs {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}

// 空 报告是否完全没有注入面。
func (r TrustReport) Empty() bool {
	return len(r.AllowRules) == 0 && r.AskRules == 0 && r.DenyRules == 0 &&
		!r.RulesBroken && len(r.Tools) == 0 && len(r.Commands) == 0 &&
		len(r.Skills) == 0 && len(r.Agents) == 0
}

