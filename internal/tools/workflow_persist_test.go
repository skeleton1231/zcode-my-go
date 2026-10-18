package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestWorkflowToolDispatch 三种调用形态经真实工具面分发：steps+saveAs、
// run（按名）、resume（运行 ID）；空请求可读报错。
func TestWorkflowToolDispatch(t *testing.T) {
	var got []WorkflowRequest
	tool := NewWorkflow(func(ctx context.Context, req WorkflowRequest) (WorkflowOutcome, error) {
		got = append(got, req)
		out := WorkflowOutcome{RunID: "run-1", SavedAs: req.SaveAs}
		if len(req.Steps) > 0 {
			out.Results = []agent.StepResult{{Step: req.Steps[0], Text: "ok"}}
		}
		return out, nil
	})

	steps := `[{"description":"扫描","kind":"research"}]`
	res := tool.Execute(context.Background(),
		[]byte(`{"steps":`+steps+`,"saveAs":"release-check"}`))
	if res.IsError {
		t.Fatalf("steps+saveAs failed: %s", res.Content)
	}
	if got[0].SaveAs != "release-check" || len(got[0].Steps) != 1 {
		t.Fatalf("req = %+v", got[0])
	}
	for _, want := range []string{"已保存工作流 release-check", "运行记录 run-1", "✓ 1. [research] 扫描"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in:\n%s", want, res.Content)
		}
	}

	res = tool.Execute(context.Background(), []byte(`{"run":"release-check"}`))
	if res.IsError || got[1].Run != "release-check" {
		t.Fatalf("run dispatch: %+v %s", got[1], res.Content)
	}

	res = tool.Execute(context.Background(), []byte(`{"resume":"20261018-1-2"}`))
	if res.IsError || got[2].Resume != "20261018-1-2" {
		t.Fatalf("resume dispatch: %+v %s", got[2], res.Content)
	}

	if res := tool.Execute(context.Background(), []byte(`{}`)); !res.IsError ||
		!strings.Contains(res.Content, "steps") || !strings.Contains(res.Content, "resume") {
		t.Fatalf("empty request: %+v", res)
	}
}
