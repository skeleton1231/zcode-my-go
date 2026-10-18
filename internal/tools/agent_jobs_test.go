package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
<<<<<<< HEAD
	"github.com/skeleton1231/zcode-my-go/internal/ai"
=======
>>>>>>> origin/main
)

// waitForJobState 轮询等终态（秒级预算——后台 goroutine 的完成是异步的）。
func waitForJobState(j *AgentJob, want string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j.Snapshot().State == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return j.Snapshot().State == want
}

func TestAgentJobsSpawnDone(t *testing.T) {
	jobs := NewAgentJobs()
<<<<<<< HEAD
	j := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		return agent.SubtaskResult{Text: "调查结论"}, nil, nil
=======
	j := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: "调查结论"}, nil
>>>>>>> origin/main
	}, "调查一下")
	if v := j.Snapshot(); v.State != AgentRunning && v.State != AgentDone {
		t.Fatalf("state = %s", v.State)
	}
	if !waitForJobState(j, AgentDone) {
		t.Fatalf("never done: %+v", j.Snapshot())
	}
	if v := j.Snapshot(); v.Text != "调查结论" {
		t.Fatalf("text = %q", v.Text)
	}
	if _, ok := jobs.Get("ag-1"); !ok {
		t.Fatal("id ag-1 not registered")
	}
}

func TestAgentJobsFailure(t *testing.T) {
	jobs := NewAgentJobs()
<<<<<<< HEAD
	j := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		return agent.SubtaskResult{}, nil, errors.New("provider down")
=======
	j := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{}, errors.New("provider down")
>>>>>>> origin/main
	}, "x")
	if !waitForJobState(j, AgentFailed) {
		t.Fatalf("never failed: %+v", j.Snapshot())
	}
	if v := j.Snapshot(); v.Err == "" {
		t.Fatal("error text missing")
	}
}

func TestAgentJobsStopAndCloseAll(t *testing.T) {
	jobs := NewAgentJobs()
<<<<<<< HEAD
	j := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		<-ctx.Done() // 挂住直到停止
		return agent.SubtaskResult{}, nil, ctx.Err()
=======
	j := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		<-ctx.Done() // 挂住直到停止
		return agent.SubtaskResult{}, ctx.Err()
>>>>>>> origin/main
	}, "长调查")
	if !jobs.Stop(j.ID) {
		t.Fatal("stop returned false")
	}
	if !waitForJobState(j, AgentStopped) {
		t.Fatalf("never stopped: %+v", j.Snapshot())
	}
	if v := j.Snapshot(); v.Text != "" {
		t.Fatalf("stopped agent has text: %q", v.Text)
	}
	if jobs.Stop("ag-99") {
		t.Fatal("unknown id stopped")
	}

	// CloseAll 全停：root 取消传播到运行中的任务。
<<<<<<< HEAD
	j2 := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		<-ctx.Done()
		return agent.SubtaskResult{}, nil, ctx.Err()
=======
	j2 := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		<-ctx.Done()
		return agent.SubtaskResult{}, ctx.Err()
>>>>>>> origin/main
	}, "另一个")
	jobs.CloseAll()
	if !waitForJobState(j2, AgentStopped) {
		t.Fatalf("CloseAll did not stop: %+v", j2.Snapshot())
	}
}

func TestAgentToolsFlow(t *testing.T) {
	jobs := NewAgentJobs()
<<<<<<< HEAD
	spawn := func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		return agent.SubtaskResult{Text: "结论：" + prompt}, nil, nil
=======
	spawn := func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: "结论：" + prompt}, nil
>>>>>>> origin/main
	}
	spawner := NewAgentTool(jobs, spawn)
	res := spawner.Execute(context.Background(), []byte(`{"prompt":"查依赖"}`))
	if res.IsError {
		t.Fatalf("spawn failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "ag-1") {
		t.Fatalf("receipt missing id: %q", res.Content)
	}

	out := NewAgentOutputTool(jobs)
	// 未命中给出既有清单。
	if res := out.Execute(context.Background(), []byte(`{"id":"nope"}`)); !res.IsError || !strings.Contains(res.Content, "ag-1") {
		t.Fatalf("unknown id: %+v", res)
	}
	// 等终态后收割结论（等 running→done 翻转）。
	j, _ := jobs.Get("ag-1")
	if !waitForJobState(j, AgentDone) {
		t.Fatal("never done")
	}
	res = out.Execute(context.Background(), []byte(`{"id":"ag-1"}`))
	if res.IsError || !strings.Contains(res.Content, "结论：查依赖") {
		t.Fatalf("output: %+v", res)
	}

	stop := NewAgentStopTool(jobs)
	if res := stop.Execute(context.Background(), []byte(`{"id":"ag-1"}`)); res.IsError {
		t.Fatalf("stop terminal agent: %s", res.Content)
	}
}
<<<<<<< HEAD

// TestAgentJobsResumeWithHistory 续跑（spec 规则 51）：终态后带历史续
// 跑——运行器收到的 prior 即首次转录，同 ID 复用，新结论覆盖旧值。
func TestAgentJobsResumeWithHistory(t *testing.T) {
	jobs := NewAgentJobs()
	var gotPrior []ai.Message
	first := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		h := append([]ai.Message(nil), prior...)
		h = append(h, ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}},
			ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "首次结论"}}})
		return agent.SubtaskResult{Text: "首次结论"}, h, nil
	}, "首次调查")
	if !waitForJobState(first, AgentDone) {
		t.Fatal("first never done")
	}

	second, err := jobs.Resume(first.ID, "追问细节", func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		gotPrior = prior
		return agent.SubtaskResult{Text: "追加结论"}, append(prior, ai.Message{Role: ai.RoleAssistant, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "追加结论"}}}), nil
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("resume changed id: %s", second.ID)
	}
	if !waitForJobState(first, AgentDone) {
		t.Fatal("resume never done")
	}
	if v := first.Snapshot(); v.Text != "追加结论" {
		t.Fatalf("text not replaced: %q", v.Text)
	}
	// 续跑收到的 prior 是首次的完整转录（用户问题 + 结论）。
	joined := ""
	for _, m := range gotPrior {
		joined += m.Text()
	}
	if !strings.Contains(joined, "首次调查") || !strings.Contains(joined, "首次结论") {
		t.Fatalf("prior history incomplete: %q", joined)
	}
}

// TestAgentJobsResumeRejectsRunning 运行中的任务拒绝续话。
func TestAgentJobsResumeRejectsRunning(t *testing.T) {
	jobs := NewAgentJobs()
	j := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		<-ctx.Done()
		return agent.SubtaskResult{}, nil, ctx.Err()
	}, "挂住")
	if _, err := jobs.Resume(j.ID, "追问", nil); err == nil ||
		!strings.Contains(err.Error(), "运行") {
		t.Fatalf("running resume accepted: %v", err)
	}
	if _, err := jobs.Resume("ag-99", "x", nil); err == nil {
		t.Fatal("unknown resume accepted")
	}
}

func TestAgentSendToolFlow(t *testing.T) {
	jobs := NewAgentJobs()
	j := jobs.Spawn(func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		return agent.SubtaskResult{Text: "首次"}, nil, nil
	}, "首查")
	if !waitForJobState(j, AgentDone) {
		t.Fatal("never done")
	}
	send := NewAgentSendTool(jobs, func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
		return agent.SubtaskResult{Text: "续话结论"}, prior, nil
	})
	res := send.Execute(context.Background(), []byte(`{"id":"ag-1","message":"追问"}`))
	if res.IsError {
		t.Fatalf("send failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "已带全部上下文续跑") {
		t.Fatalf("receipt: %q", res.Content)
	}
	if !waitForJobState(j, AgentDone) || j.Snapshot().Text != "续话结论" {
		t.Fatalf("resumed answer missing: %+v", j.Snapshot())
	}
	if res := send.Execute(context.Background(), []byte(`{"id":"nope","message":"x"}`)); !res.IsError {
		t.Fatal("unknown id accepted")
	}
	if res := send.Execute(context.Background(), []byte(`{"id":"ag-1"}`)); !res.IsError {
		t.Fatal("missing message accepted")
	}
}
=======
>>>>>>> origin/main
