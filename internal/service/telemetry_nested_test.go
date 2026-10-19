package service

// 嵌套循环遥测（规则 91 扩展）：子代理与工作流步骤的循环也产 span
//（subagent.turn / workflow.step），与主回合 agent.turn 名字区分。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func telemSvc(t *testing.T) (*AgentService, string, string) {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"telemetry": map[string]any{"enabled": true}})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return svc, info.ID, dir
}

func spansOf(t *testing.T, dataDir string) []map[string]any {
	t.Helper()
	path := filepath.Join(dataDir, "traces", "trace-"+time.Now().Format("20060102")+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read traces: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func TestSubagentTelemetrySpan(t *testing.T) {
	svc, id, dataDir := telemSvc(t)
	p := &taskScriptProvider{turns: []string{"", "收尾"}}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(id, "调查一下", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range spansOf(t, dataDir) {
		names[s["name"].(string)] = true
	}
	if !names["agent.turn"] {
		t.Fatalf("main turn span missing: %v", names)
	}
	if !names["subagent.turn"] {
		t.Fatalf("subagent span missing: %v", names)
	}
}

func TestWorkflowTelemetrySpan(t *testing.T) {
	svc, id, dataDir := telemSvc(t)
	// 步骤要带 description（WorkflowStep 的提示来源）——旧共享夹具
	// 的 prompt 字段会让校验短路、步骤根本不跑（旧用量断言其实来自
	// 主回合）。
	p := &wfStepProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(id, "按计划执行", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	for _, s := range spansOf(t, dataDir) {
		if n, _ := s["name"].(string); n == "workflow.step" {
			return // 存在即够——属性面由 loop 层测试覆盖
		}
	}
	names := map[string]bool{}
	for _, s := range spansOf(t, dataDir) {
		names[s["name"].(string)] = true
	}
	t.Fatalf("workflow.step span missing: %v", names)
}

// wfStepProvider：回合 1 发带合法步骤的 workflow 调用 → 步骤子任务
// （回合 2）→ 主循环收尾（回合 3+）。
type wfStepProvider struct{ n int }

func (p *wfStepProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.n++
	out := make(chan ai.StreamEvent, 4)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	switch p.n {
	case 1:
		out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "wf1", ToolCallName: "workflow"}
		out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "wf1", ToolCallArgs: `{"steps":[{"name":"s1","kind":"research","description":"调查"}]}`}
	case 2:
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "步骤结论"}
	default:
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "收尾"}
	}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}
