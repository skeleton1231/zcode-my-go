package tools

// 后台子代理注册表（ZCode runtime-task registry 的 Zgo 形态，spec 规
// 则 49）：dispatch/task 是前台阻塞的扇出——后台形态让父会话继续工作，
// 子代理在独立 goroutine 跑完落状态，经 agent_output 收割、agent_stop
// 停止。生命周期与会话一致（DeleteSession/Close 全停），不随工具调用
// 的 ctx 取消而夭折。

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// 后台子代理的状态。
const (
	AgentRunning = "running"
	AgentDone    = "done"
	AgentFailed  = "failed"
	AgentStopped = "stopped"
)

// AgentJob 是一个后台子代理的注册项与终态。
type AgentJob struct {
	ID      string
	Prompt  string
	started time.Time
	cancel  context.CancelFunc

	mu    sync.Mutex
	state string
	text  string // 结论（终态）
	err   string
}

// AgentJobView 是注册项的只读快照（UI/工具输出用）。
type AgentJobView struct {
	ID       string
	Prompt   string
	State    string
	Text     string
	Err      string
	Duration time.Duration
}

// Snapshot 拍只读快照。
func (j *AgentJob) Snapshot() AgentJobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	return AgentJobView{ID: j.ID, Prompt: j.Prompt, State: j.state,
		Text: j.text, Err: j.err, Duration: time.Since(j.started)}
}

// AgentJobs 是会话作用域的后台子代理注册表。root context 让 Close 一
// 键全停（会话删除/服务关闭不留僵尸循环）。
type AgentJobs struct {
	mu   sync.Mutex
	root context.Context
	end  context.CancelFunc
	jobs map[string]*AgentJob
	seq  int
}

// NewAgentJobs 构建注册表。
func NewAgentJobs() *AgentJobs {
	root, end := context.WithCancel(context.Background())
	return &AgentJobs{root: root, end: end, jobs: map[string]*AgentJob{}}
}

// Spawn 启动一个后台子代理并立即返回注册项：run 在独立 goroutine 执
// 行，ctx 派生自注册表根（Close 全停），不随调用方取消。
func (a *AgentJobs) Spawn(run func(ctx context.Context, prompt string) (agent.SubtaskResult, error), prompt string) *AgentJob {
	a.mu.Lock()
	a.seq++
	id := "ag-" + strconv.Itoa(a.seq)
	ctx, cancel := context.WithCancel(a.root)
	j := &AgentJob{ID: id, Prompt: prompt, started: time.Now(), cancel: cancel, state: AgentRunning}
	a.jobs[id] = j
	a.mu.Unlock()

	go func() {
		res, err := run(ctx, prompt)
		j.mu.Lock()
		defer j.mu.Unlock()
		switch {
		case err != nil && ctx.Err() != nil:
			j.state = AgentStopped
		case err != nil:
			j.state, j.err = AgentFailed, err.Error()
		default:
			j.state, j.text = AgentDone, res.Text
		}
	}()
	return j
}

// Get 查注册项。
func (a *AgentJobs) Get(id string) (*AgentJob, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, ok := a.jobs[id]
	return j, ok
}

// IDs 列出全部注册项 ID（agent_output 未命中时的自纠提示）。
func (a *AgentJobs) IDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.jobs))
	for id := range a.jobs {
		out = append(out, id)
	}
	return out
}

// Stop 停一个子代理（终态保持可读——结论为空、状态 stopped）。
func (a *AgentJobs) Stop(id string) bool {
	j, ok := a.Get(id)
	if !ok {
		return false
	}
	j.cancel()
	return true
}

// CloseAll 全停（会话删除/服务关闭）；注册项保留可读（收割已完成的）。
func (a *AgentJobs) CloseAll() { a.end() }
