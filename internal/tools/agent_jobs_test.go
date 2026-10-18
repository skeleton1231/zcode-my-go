package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
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
	j := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: "调查结论"}, nil
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
	j := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{}, errors.New("provider down")
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
	j := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		<-ctx.Done() // 挂住直到停止
		return agent.SubtaskResult{}, ctx.Err()
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
	j2 := jobs.Spawn(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		<-ctx.Done()
		return agent.SubtaskResult{}, ctx.Err()
	}, "另一个")
	jobs.CloseAll()
	if !waitForJobState(j2, AgentStopped) {
		t.Fatalf("CloseAll did not stop: %+v", j2.Snapshot())
	}
}

func TestAgentToolsFlow(t *testing.T) {
	jobs := NewAgentJobs()
	spawn := func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
		return agent.SubtaskResult{Text: "结论：" + prompt}, nil
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
