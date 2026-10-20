package service

// 会话规则固化（spec 规则 105）：会话内攒的「总是允许」规则随会话结
// 束消散——本文件把它们渲染成 .zgorules.json 建议（version:1 + rules，
// 可被 ParseRuleset 直接解析），用户审阅后落盘即成为项目规则（经信
// 任门生效）。只导出会话增量，不掺现有项目规则——diff 视角。

import (
	"encoding/json"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// exportRule 是 .zgorules.json 的规则形态（ID 是会话簿记字段，落盘
// 不带）。
type exportRule struct {
	Tool     string             `json:"tool"`
	Args     string             `json:"args,omitempty"`
	Behavior permission.Behavior `json:"behavior"`
}

// SuggestProjectRules 把会话的「总是允许」规则渲染成 .zgorules.json
// 文本（spec 规则 105）。无规则返回空串（调用方给提示）。
func (s *AgentService) SuggestProjectRules(sessionID string) (string, error) {
	rules := s.SessionPermissionRules(sessionID)
	if len(rules) == 0 {
		return "", nil
	}
	out := struct {
		Version int          `json:"version"`
		Rules   []exportRule `json:"rules"`
	}{Version: 1}
	for _, r := range rules {
		out.Rules = append(out.Rules, exportRule{Tool: r.Tool, Args: r.Args, Behavior: r.Behavior})
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
