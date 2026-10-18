package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestBashBackgroundRemoteLifecycle 验证后台 shell 的远程形态（spec 规则
// 19）：bash {background:true} 在 ssh:// 工作区上经 RemoteFS.StartBackground
// 起远端通道（而非落回本地执行——remoteWS 的 Root 是本地不存在的路径，
// 落回本地必失败）、bash_output 流式排空、bash_kill 停止。
func TestBashBackgroundRemoteLifecycle(t *testing.T) {
	ws, _ := remoteWS(t)
	jobs := NewJobs()
	defer jobs.KillAll()
	b := NewBash(ws, jobs)

	cmd := "echo bg-remote && sleep 30"
	if runtime.GOOS == "windows" {
		cmd = "echo bg-remote & ping -n 30 127.0.0.1 > nul"
	}
	res := b.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"command":%q,"background":true}`, cmd)))
	if res.IsError {
		t.Fatalf("background start: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[job1]") {
		t.Fatalf("start result = %s", res.Content)
	}

	// 输出经 bash_output 增量可见（10s 预算）。
	out := NewBashOutput(jobs)
	deadline := time.Now().Add(10 * time.Second)
	var poll agent.ToolResult
	for time.Now().Before(deadline) {
		poll = out.Execute(context.Background(), json.RawMessage(`{"job_id":"job1"}`))
		if strings.Contains(poll.Content, "bg-remote") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(poll.Content, "bg-remote") {
		t.Fatalf("no streamed output via bash_output: %s", poll.Content)
	}
	if !strings.Contains(poll.Content, "运行中") {
		t.Fatalf("job should still be running: %s", poll.Content)
	}

	// bash_kill 停止远端任务（关通道即杀），job 退出。
	kill := NewBashKill(jobs)
	if kr := kill.Execute(context.Background(), json.RawMessage(`{"job_id":"job1"}`)); kr.IsError {
		t.Fatalf("kill: %s", kr.Content)
	}
	job, ok := jobs.Get("job1")
	if !ok {
		t.Fatal("job1 missing")
	}
	deadline = time.Now().Add(10 * time.Second)
	for running, _ := job.Status(); running && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		running, _ = job.Status()
	}
	if running, _ := job.Status(); running {
		t.Fatal("remote job still running after bash_kill")
	}
}
