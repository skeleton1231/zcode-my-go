package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// bashJobCapBytes bounds a background job's retained output (ZCode's
// bash-background output cap): beyond it the oldest bytes drop.
const bashJobCapBytes = 256 << 10

// Job is one background shell process (ZCode's bash background task):
// output streams into a capped buffer the model drains incrementally.
type Job struct {
	ID      string
	Command string

	mu      sync.Mutex
	output  bytes.Buffer
	read    int // bytes already handed to bash_output
	cmd     commandProcess
	done    chan struct{}
	exitErr error
}

// commandProcess abstracts *exec.Cmd enough for jobs and fakes.
type commandProcess interface {
	Start() error
	KillTree() error
	Wait() error
}

// Status reports the job's running state and exit error.
func (j *Job) Status() (running bool, exitErr error) {
	select {
	case <-j.done:
		j.mu.Lock()
		defer j.mu.Unlock()
		return false, j.exitErr
	default:
		return true, nil
	}
}

// ReadNew returns output accumulated since the last read.
func (j *Job) ReadNew() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	data := j.output.Bytes()
	if j.read > len(data) {
		j.read = len(data) // 截断把 read 推前了
	}
	fresh := string(data[j.read:])
	j.read = len(data)
	return fresh
}

// Kill terminates the process tree.
func (j *Job) Kill() error {
	return j.cmd.KillTree()
}

// append records output under the cap.
func (j *Job) append(p []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.output.Write(p)
	if j.output.Len() > bashJobCapBytes {
		trimmed := j.output.Bytes()[j.output.Len()-bashJobCapBytes:]
		trimmed = append([]byte(nil), trimmed...)
		j.output.Reset()
		j.output.Write(trimmed)
		j.read -= len(trimmed)
		if j.read < 0 {
			j.read = 0
		}
	}
}

// Jobs is the session-scoped background job registry.
type Jobs struct {
	mu   sync.Mutex
	next int
	jobs map[string]*Job
}

// NewJobs builds an empty registry.
func NewJobs() *Jobs { return &Jobs{jobs: map[string]*Job{}} }

// Start launches a command in the background and returns its job.
func (js *Jobs) Start(cmd commandProcess, command string) (*Job, error) {
	js.mu.Lock()
	js.next++
	id := fmt.Sprintf("job%d", js.next)
	job := &Job{ID: id, Command: command, cmd: cmd, done: make(chan struct{})}
	js.jobs[id] = job
	js.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		job.exitErr = cmd.Wait()
		close(job.done)
	}()
	return job, nil
}

// Get returns a job by id.
func (js *Jobs) Get(id string) (*Job, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	j, ok := js.jobs[id]
	return j, ok
}

// KillAll terminates every running job (session end, app quit).
func (js *Jobs) KillAll() {
	js.mu.Lock()
	jobs := make([]*Job, 0, len(js.jobs))
	for _, j := range js.jobs {
		jobs = append(jobs, j)
	}
	js.mu.Unlock()
	for _, j := range jobs {
		_ = j.Kill()
	}
}

// --- tools ----------------------------------------------------------------

// BashOutput drains a background job's new output (ZCode's bash_output).
type BashOutput struct{ jobs *Jobs }

// NewBashOutput builds the tool.
func NewBashOutput(jobs *Jobs) *BashOutput { return &BashOutput{jobs: jobs} }

// Name implements agent.Tool.
func (b *BashOutput) Name() string { return "bash_output" }

// Description implements agent.Tool.
func (b *BashOutput) Description() string {
	return "Read new output from a background job started with bash " +
		"{background:true}. Each call returns what accumulated since the " +
		"last read, plus the job's running state."
}

// Parameters implements agent.Tool.
func (b *BashOutput) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"job_id": prop("string", "The job id bash returned"),
	}, "job_id")
}

// Execute implements agent.Tool.
func (b *BashOutput) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		JobID string `json:"job_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	job, ok := b.jobs.Get(a.JobID)
	if !ok {
		return errResult("no such job %q", a.JobID)
	}
	running, exitErr := job.Status()
	var out strings.Builder
	fmt.Fprintf(&out, "[%s] %s\n", job.ID, job.Command)
	if running {
		out.WriteString("运行中\n")
	} else if exitErr != nil {
		fmt.Fprintf(&out, "已退出：%v\n", exitErr)
	} else {
		out.WriteString("已退出（成功）\n")
	}
	if fresh := job.ReadNew(); fresh != "" {
		out.WriteString(truncateBytes([]byte(fresh), bashHeadTail, bashHeadTail))
	} else {
		out.WriteString("(无新输出)\n")
	}
	return agent.ToolResult{Content: out.String()}
}

// Capability implements agent.Capabler: reading output is safe.
func (b *BashOutput) Capability() permission.Capability {
	return permission.Capability{ReadOnly: true, Risk: permission.RiskLow}
}

// BashKill stops a background job's whole process tree.
type BashKill struct{ jobs *Jobs }

// NewBashKill builds the tool.
func NewBashKill(jobs *Jobs) *BashKill { return &BashKill{jobs: jobs} }

// Name implements agent.Tool.
func (b *BashKill) Name() string { return "bash_kill" }

// Description implements agent.Tool.
func (b *BashKill) Description() string {
	return "Stop a background job (its whole process tree). Use when a " +
		"dev server or watcher is no longer needed."
}

// Parameters implements agent.Tool.
func (b *BashKill) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"job_id": prop("string", "The job id bash returned"),
	}, "job_id")
}

// Execute implements agent.Tool.
func (b *BashKill) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		JobID string `json:"job_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	job, ok := b.jobs.Get(a.JobID)
	if !ok {
		return errResult("no such job %q", a.JobID)
	}
	if err := job.Kill(); err != nil {
		return errResult("killing %s: %v", a.JobID, err)
	}
	select {
	case <-job.done:
	case <-time.After(3 * time.Second):
	}
	return okResult("已停止 %s", a.JobID)
}

// Capability implements agent.Capabler: stopping a job the agent started.
func (b *BashKill) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}
