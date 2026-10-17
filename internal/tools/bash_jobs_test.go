package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// fakeProcess is a commandProcess double whose output the test feeds.
type fakeProcess struct {
	started int
	killed  int
	waitErr error
	done    chan struct{}
}

func (f *fakeProcess) Start() error {
	f.started++
	f.done = make(chan struct{})
	return nil
}
func (f *fakeProcess) KillTree() error {
	f.killed++
	close(f.done)
	return nil
}
func (f *fakeProcess) Wait() error { <-f.done; return f.waitErr }

func TestBackgroundJobLifecycle(t *testing.T) {
	jobs := NewJobs()
	proc := &fakeProcess{}
	job, err := jobs.Start(proc, "serve")
	if err != nil {
		t.Fatal(err)
	}
	if proc.started != 1 {
		t.Fatalf("started = %d", proc.started)
	}
	if running, _ := job.Status(); !running {
		t.Fatal("job not running")
	}

	// 增量输出：先到先读，后来再读只拿新的。
	job.append([]byte("first\n"))
	if got := job.ReadNew(); got != "first\n" {
		t.Fatalf("first read = %q", got)
	}
	job.append([]byte("second\n"))

	// bash_output 工具的汇报。
	res := NewBashOutput(jobs).Execute(context.Background(), json.RawMessage(
		`{"job_id":"`+job.ID+`"}`))
	if res.IsError || !strings.Contains(res.Content, "运行中") || !strings.Contains(res.Content, "second") {
		t.Fatalf("output tool = %+v", res)
	}

	// bash_kill 停止整树。
	res = NewBashKill(jobs).Execute(context.Background(), json.RawMessage(
		`{"job_id":"`+job.ID+`"}`))
	if res.IsError || proc.killed != 1 {
		t.Fatalf("kill tool = %+v killed=%d", res, proc.killed)
	}
	// 等待 Wait goroutine 收尾后状态翻转。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if running, _ := job.Status(); !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if running, _ := job.Status(); running {
		t.Fatal("job still running after kill")
	}
}

func TestBackgroundJobViaBashTool(t *testing.T) {
	w := testWorkspace(t)
	bash := NewBash(w, NewJobs())
	echo := "echo hi"
	if res := bash.Execute(context.Background(), json.RawMessage(
		`{"command":"`+echo+`","background":true}`)); res.IsError {
		t.Fatalf("background start: %s", res.Content)
	} else if !strings.Contains(res.Content, "后台任务已启动") || !strings.Contains(res.Content, "bash_output") {
		t.Fatalf("start result = %s", res.Content)
	}
	// 短命令很快退出并留下输出。
	deadline := time.Now().Add(5 * time.Second)
	var out agent.ToolResult
	for time.Now().Before(deadline) {
		// job1 是该注册表的首个任务。
		out = NewBashOutput(bash.jobs).Execute(context.Background(), json.RawMessage(`{"job_id":"job1"}`))
		if strings.Contains(out.Content, "hi") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.Content, "hi") {
		t.Fatalf("job output missing: %s", out.Content)
	}
}

func TestJobOutputCap(t *testing.T) {
	jobs := NewJobs()
	job, err := jobs.Start(&fakeProcess{}, "x")
	if err != nil {
		t.Fatal(err)
	}
	job.ReadNew() // 清空初始
	big := strings.Repeat("a", 300<<10)
	job.append([]byte(big))
	if got := len(job.ReadNew()); got > bashJobCapBytes+1024 {
		t.Fatalf("capped read = %d", got)
	}
}

func TestJobUnknownID(t *testing.T) {
	jobs := NewJobs()
	if res := NewBashOutput(jobs).Execute(context.Background(), json.RawMessage(`{"job_id":"nope"}`)); !res.IsError {
		t.Fatal("unknown id accepted")
	}
	if res := NewBashKill(jobs).Execute(context.Background(), json.RawMessage(`{"job_id":"nope"}`)); !res.IsError {
		t.Fatal("unknown id accepted for kill")
	}
}
