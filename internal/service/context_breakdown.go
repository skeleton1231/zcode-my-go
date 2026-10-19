package service

// 上下文构成观测（spec 规则 79）：「token 都花哪了」一命令可见——与
// 下一请求同源的分段测量（系统提示稳定前缀/git 摘要/记忆/技能/子代
// 理清单/工具声明/转录），字节口径（token 估算只做参照不做断言）。

import (
	"encoding/json"
	"strconv"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agents"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/prompt"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
)

// ContextPart 是构成的一段。
type ContextPart struct {
	Name  string
	Bytes int
	Note  string // 计数/说明（如「N 条消息」「M 个工具」）
}

// ContextBreakdown 测量会话下一请求的构成分段。与 loop 装配同源
// （buildTools/Section 家族同一函数——测的即发的）。
func (s *AgentService) ContextBreakdown(sessionID, workspace string) []ContextPart {
	if _, ok := s.mgr.Get(sessionID); !ok {
		return nil
	}
	ws := s.workspaceFor(sessionID, workspace)
	read := contextReader(ws)
	parts := []ContextPart{
		{Name: "system·stable", Bytes: len(prompt.SystemStableFS(workspace, read)),
			Note: "identity/env/project context（缓存前缀）"},
		{Name: "system·git", Bytes: len(prompt.SystemVolatile(workspace)),
			Note: "git 摘要（易变段——每请求重算）"},
		{Name: "memory", Bytes: len(s.memoryFor(workspace).Section()),
			Note: "工作区记忆注入（4KB 尾窗）"},
		{Name: "skills", Bytes: len(skills.Section(skills.Discover(s.skillRoots(sessionID, workspace)), skills.DefaultMetaBudget)),
			Note: "可用技能清单"},
		{Name: "agents", Bytes: len(agents.Section(agents.Discover(s.agentRoots(sessionID, workspace)), agents.DefaultMetaBudget)),
			Note: "自定义子代理清单"},
	}
	if s.buildTools != nil {
		if decls := agent.Declarations(s.buildTools(workspace, sessionID)); len(decls) > 0 {
			if raw, err := json.Marshal(decls); err == nil {
				parts = append(parts, ContextPart{Name: "tools", Bytes: len(raw),
					Note: itoa(len(decls)) + " 个工具声明"})
			}
		}
	}
	msgs, _ := s.Messages(sessionID)
	bytes, users := 0, 0
	for _, m := range msgs {
		if m.Role == ai.RoleUser {
			users++
		}
		for _, b := range m.Blocks {
			raw, err := json.Marshal(b)
			if err == nil {
				bytes += len(raw)
			}
		}
	}
	parts = append(parts, ContextPart{Name: "transcript", Bytes: bytes,
		Note: itoa(len(msgs)) + " 条消息（" + itoa(users) + " 用户回合）"})
	return parts
}

func itoa(n int) string { return strconv.Itoa(n) }
