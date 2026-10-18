package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// agentTool 启动一个后台只读子代理（ZCode 的 Agent 工具，spec 规则
// 49）：立即返回 agentId，父会话继续工作；agent_output 收割结论、
// agent_stop 停止。Spawn 是单任务运行器（RunSubtask 形态），由服务
// 接线（provider 与工作区同源）。
type agentTool struct {
	jobs  *AgentJobs
<<<<<<< HEAD
	spawn AgentRunFunc
}

// NewAgentTool 构建后台子代理工具。
func NewAgentTool(jobs *AgentJobs, spawn AgentRunFunc) agent.Tool {
=======
	spawn func(ctx context.Context, prompt string) (agent.SubtaskResult, error)
}

// NewAgentTool 构建后台子代理工具。
func NewAgentTool(jobs *AgentJobs, spawn func(ctx context.Context, prompt string) (agent.SubtaskResult, error)) agent.Tool {
>>>>>>> origin/main
	return &agentTool{jobs: jobs, spawn: spawn}
}

// Name implements agent.Tool.
func (t *agentTool) Name() string { return "agent" }

// Description implements agent.Tool.
func (t *agentTool) Description() string {
	return "Spawn a background read-only research subagent and keep working. " +
		"Returns an agentId immediately; collect its answer later with " +
		"agent_output (poll while it runs), stop it with agent_stop. Use for " +
		"long investigations you don't want to block on; for synchronous " +
		"fan-out use dispatch."
}

// Parameters implements agent.Tool.
func (t *agentTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt": prop("string", "A self-contained research question (read-only investigation)"),
	}, "prompt")
}

// Execute implements agent.Tool.
func (t *agentTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Prompt string `json:"prompt"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return errResult("prompt is required")
	}
	j := t.jobs.Spawn(t.spawn, strings.TrimSpace(a.Prompt))
	return okResult("子代理 %s 已启动：%s\n用 agent_output {\"id\":\"%s\"} 收割结论（运行中会报告进度），agent_stop 停止。",
		j.ID, firstLineOf(a.Prompt), j.ID)
}

// Capability implements agent.Capabler：子代理只读（读工具面），但产生
// 后台模型用量——中等风险过一次询问，会话「总是允许」可积累。
func (t *agentTool) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// agentOutputTool 收割后台子代理的状态与结论。
type agentOutputTool struct{ jobs *AgentJobs }

// NewAgentOutputTool 构建收割工具。
func NewAgentOutputTool(jobs *AgentJobs) agent.Tool { return &agentOutputTool{jobs} }

// Name implements agent.Tool.
func (o *agentOutputTool) Name() string { return "agent_output" }

// Description implements agent.Tool.
func (o *agentOutputTool) Description() string {
	return "Check a background subagent: running (with elapsed time) or its " +
		"final answer. Poll this while working on other things."
}

// Parameters implements agent.Tool.
func (o *agentOutputTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"id": prop("string", "The agentId returned by the agent tool"),
	}, "id")
}

// Execute implements agent.Tool.
func (o *agentOutputTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	j, ok := o.jobs.Get(strings.TrimSpace(a.ID))
	if !ok {
		ids := o.jobs.IDs()
		if len(ids) == 0 {
			return errResult("没有后台子代理（agent 工具启动）")
		}
		return errResult("未知的子代理 %s；现有：%s", a.ID, strings.Join(ids, ", "))
	}
	v := j.Snapshot()
	switch v.State {
	case AgentRunning:
		return okResult("子代理 %s 运行中（%s）：%s", v.ID, shortDur(v.Duration), firstLineOf(v.Prompt))
	case AgentDone:
		if strings.TrimSpace(v.Text) == "" {
			return okResult("子代理 %s 完成（无结论文本）", v.ID)
		}
		return agent.ToolResult{Content: v.Text}
	case AgentFailed:
		return errResult("子代理 %s 失败：%s", v.ID, v.Err)
	default: // stopped
		return okResult("子代理 %s 已被停止（无结论）", v.ID)
	}
}

// Capability implements agent.Capabler：读注册表是只读的。
func (o *agentOutputTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// agentStopTool 停止后台子代理。
type agentStopTool struct{ jobs *AgentJobs }

// NewAgentStopTool 构建停止工具。
func NewAgentStopTool(jobs *AgentJobs) agent.Tool { return &agentStopTool{jobs} }

// Name implements agent.Tool.
func (s *agentStopTool) Name() string { return "agent_stop" }

// Description implements agent.Tool.
func (s *agentStopTool) Description() string {
	return "Stop a background subagent by agentId. Terminal agents stay " +
		"readable (empty answer)."
}

// Parameters implements agent.Tool.
func (s *agentStopTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"id": prop("string", "The agentId to stop"),
	}, "id")
}

// Execute implements agent.Tool.
func (s *agentStopTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	id := strings.TrimSpace(a.ID)
	if !s.jobs.Stop(id) {
		return errResult("未知的子代理 %s", id)
	}
	return okResult("已请求停止子代理 %s", id)
}

// Capability implements agent.Capabler：停止是低风险控制面。
func (s *agentStopTool) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// firstLineOf 取首行（回执里压缩提示词）。
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// shortDur 人读时长。
func shortDur(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return fmt.Sprintf("%.0fm", d.Minutes())
}
<<<<<<< HEAD

// agentSendTool 续话终态子代理（ZCode SendMessage 的落地，spec 规则
// 51）：追加用户消息、带既有历史后台重跑——同 ID 复用，收割面不变。
type agentSendTool struct {
	jobs   *AgentJobs
	resume AgentRunFunc
}

// NewAgentSendTool 构建续话工具。
func NewAgentSendTool(jobs *AgentJobs, resume AgentRunFunc) agent.Tool {
	return &agentSendTool{jobs: jobs, resume: resume}
}

// Name implements agent.Tool.
func (s *agentSendTool) Name() string { return "agent_send" }

// Description implements agent.Tool.
func (s *agentSendTool) Description() string {
	return "Send a follow-up message to a finished background subagent: it " +
		"resumes in the background with its full prior context (investigation, " +
		"results, answer) and the same agentId. Use for follow-up questions " +
		"instead of re-spawning with a re-explained prompt. A running agent " +
		"cannot receive messages — wait for agent_output or stop it first."
}

// Parameters implements agent.Tool.
func (s *agentSendTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"id":      prop("string", "The agentId from the agent tool"),
		"message": prop("string", "The follow-up question or instruction"),
	}, "id", "message")
}

// Execute implements agent.Tool.
func (s *agentSendTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	id := strings.TrimSpace(a.ID)
	message := strings.TrimSpace(a.Message)
	if id == "" || message == "" {
		return errResult("id and message are both required")
	}
	j, err := s.jobs.Resume(id, message, s.resume)
	if err != nil {
		if _, known := s.jobs.Get(id); !known {
			ids := s.jobs.IDs()
			if len(ids) == 0 {
				return errResult("%v（还没有后台子代理）", err)
			}
			return errResult("%v；现有：%s", err, strings.Join(ids, ", "))
		}
		return errResult("%v", err)
	}
	return okResult("子代理 %s 已带全部上下文续跑：%s\n用 agent_output 收割新结论。", j.ID, firstLineOf(message))
}

// Capability implements agent.Capabler：与 agent 工具同款——后台模型
// 用量，中等风险。
func (s *agentSendTool) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}
=======
>>>>>>> origin/main
