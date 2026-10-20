package service

// /perms why 的判定预演（spec 规则 129）：对一次假想调用走与 loop gate
// 完全相同的判定链（同一 Check + 同一 capability 路径 + 同一参数摘要），
// 展示结果与来源——调试「为什么这个工具还要问 / 为什么被拒」。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// PermWhy 预演一次调用的权限判定，返回人读的解释（工具面之外零副作用
// ——不执行工具、不写会话规则）。args 可为空（无参形态按能力基线判）。
func (s *AgentService) PermWhy(sessionID, tool string, rawArgs json.RawMessage) (string, error) {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("perms why: 会话 %s 不存在", sessionID)
	}
	workspace := sess.Info().Workspace
	if s.buildTools == nil {
		return "", fmt.Errorf("perms why: 工具面未接线")
	}
	var target agent.Tool
	for _, t := range s.buildTools(workspace, sessionID) {
		if t != nil && t.Name() == tool {
			target = t
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("perms why: 工具 %q 不在本会话工具面（/mcp、gopls 等按环境收窄）", tool)
	}
	perm := s.mgr.SessionPermissions(sessionID)
	if perm == nil {
		return "bypass：本会话未启用权限系统（完全旁路）", nil
	}
	args := ""
	if len(rawArgs) > 0 {
		args = string(rawArgs)
	}
	d := perm.Check(
		permission.Context{Tool: tool, Args: agent.SummarizeArgs(rawArgs)},
		agent.CapabilityFor(target, args))
	return renderDecision(tool, args, d), nil
}

// renderDecision 渲染判定链结果：行为 + 来源 + 理由。
func renderDecision(tool, args string, d permission.Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s → %s", tool, behaviorLabel(d.Behavior))
	if args != "" && len(args) > 0 {
		fmt.Fprintf(&b, "（参数：%s）", agent.SummarizeArgs(json.RawMessage(args)))
	}
	b.WriteString("\n来源：")
	switch {
	case d.RuleID != "":
		fmt.Fprintf(&b, "规则 %s（%s）", d.RuleID, d.Reason)
	case d.Escalated:
		fmt.Fprintf(&b, "能力兜底（%s）", d.Reason)
	default:
		fmt.Fprintf(&b, "%s", d.Reason)
	}
	fmt.Fprintf(&b, "\n风险：%s", d.Risk)
	return b.String()
}

func behaviorLabel(b permission.Behavior) string {
	switch b {
	case permission.Allow:
		return "allow（自动放行）"
	case permission.Deny:
		return "deny（拒绝）"
	default:
		return "ask（询问用户）"
	}
}
