package e2e

// 工作流持久化与复用（spec 规则 48）的全栈 e2e：saveAs 落盘定义 → 同
// 会话 {run:} 从存储加载执行 → 运行记录可查（真实 dataDir 往返）。

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func TestE2EWorkflowSaveRunAndRecords(t *testing.T) {
	// 五个脚本回合：①saveAs 工具调用 ②子任务响应 ③文本收尾第一次
	// Ask ④run 工具调用 ⑤子任务响应（其后重放收尾）。
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "workflow", Args: `{"steps":[{"description":"扫描依赖","kind":"research"}],"saveAs":"nightly-check"}`}}},
		Turn{Text: "依赖清单已扫描"},
		Turn{Text: "已保存"},
		Turn{ToolCalls: []ToolCall{{Name: "workflow", Args: `{"run":"nightly-check"}`}}},
		Turn{Text: "二次执行完成"},
	)

	events := h.Ask("保存并检查")
	saveOK, runID := false, ""
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "workflow" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "已保存工作流 nightly-check") {
				saveOK = true
			}
			if i := strings.Index(ev.ResultContent, "运行记录 "); i >= 0 {
				line := ev.ResultContent[i+len("运行记录 "):]
				if j := strings.IndexByte(line, '\n'); j >= 0 {
					line = line[:j]
				}
				runID = line
			}
		}
	}
	if !saveOK {
		t.Fatal("saveAs never reported")
	}
	if runID == "" {
		t.Fatal("run ID never reported")
	}

	// 存储面：定义已落盘，运行记录已完成。
	store := h.Svc.WorkflowStore()
	defs := store.List()
	if len(defs) != 1 || defs[0].Name != "nightly-check" || len(defs[0].Steps) != 1 {
		t.Fatalf("defs = %+v", defs)
	}
	rec, err := store.GetRun(runID)
	if err != nil || rec.Status != "completed" || !rec.Steps[0].Done {
		t.Fatalf("record = %+v err = %v", rec, err)
	}
	firstRuns := len(store.ListRuns(0))

	// 第二次调用：{run:} 从磁盘加载定义并真实执行。
	events = h.Ask("再跑一次")
	ran := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "workflow" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "扫描依赖") {
				ran = true
			}
		}
	}
	if !ran {
		t.Fatal("saved workflow did not execute")
	}
	if got := len(store.ListRuns(0)); got != firstRuns+1 {
		t.Fatalf("runs = %d, want %d", got, firstRuns+1)
	}
}
