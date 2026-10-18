package tools

// 后台子代理注册表（ZCode runtime-task registry 的 Zgo 形态，spec 规
// 则 49）：dispatch/task 是前台阻塞的扇出——后台形态让父会话继续工作，
// 子代理在独立 goroutine 跑完落状态，经 agent_output 收割、agent_stop
// 停止。生命周期与会话一致（DeleteSession/Close 全停），不随工具调用
// 的 ctx 取消而夭折。

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// fmtErrUnknownAgent 构造未命中错误（agent_output/agent_send 共用）。
func fmtErrUnknownAgent(id string) error {
	return fmt.Errorf("未知的子代理 %s", id)
}

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

	mu      sync.Mutex
	state   string
	text    string // 结论（终态）
	err     string
	history []ai.Message // 子代理转录（续跑的上下文，spec 规则 51）
}

// AgentRunFunc 是子代理运行器：prior 为空即全新任务，非空即续跑（历史
// 由注册表提供）；id 是注册表分配的代理标识（升级提问的上下文前缀，
// spec 规则 62）；返回终态历史供再次续跑。
type AgentRunFunc func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error)

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
func (a *AgentJobs) Spawn(run AgentRunFunc, prompt string) *AgentJob {
	a.mu.Lock()
	a.seq++
	id := "ag-" + strconv.Itoa(a.seq)
	ctx, cancel := context.WithCancel(a.root)
	j := &AgentJob{ID: id, Prompt: prompt, started: time.Now(), cancel: cancel, state: AgentRunning}
	a.jobs[id] = j
	a.mu.Unlock()

	go a.runJob(ctx, j, run, prompt, nil)
	return j
}

// Resume 续跑一个终态子代理（spec 规则 51）：追加用户消息、带着既有
// 历史重新进入后台运行——同 ID 复用（收割面不变）。运行中的任务拒绝
// 续跑（等完成或先停止）。
func (a *AgentJobs) Resume(id, message string, run AgentRunFunc) (*AgentJob, error) {
	a.mu.Lock()
	j, ok := a.jobs[id]
	if !ok {
		a.mu.Unlock()
		return nil, fmtErrUnknownAgent(id)
	}
	// 终态检查与翻转须原子（并发的 Resume 只能有一个生效）。
	j.mu.Lock()
	if j.state == AgentRunning {
		j.mu.Unlock()
		a.mu.Unlock()
		return nil, fmt.Errorf("子代理 %s 还在运行——等 agent_output 收割，或先 agent_stop", id)
	}
	j.state = AgentRunning
	j.text, j.err = "", ""
	j.started = time.Now()
	ctx, cancel := context.WithCancel(a.root)
	j.cancel = cancel
	history := append([]ai.Message(nil), j.history...)
	j.mu.Unlock()
	a.mu.Unlock()

	go a.runJob(ctx, j, run, message, history)
	return j, nil
}

// runJob 执行一次任务/续跑并落终态（含历史）。
func (a *AgentJobs) runJob(ctx context.Context, j *AgentJob, run AgentRunFunc, prompt string, prior []ai.Message) {
	res, history, err := run(ctx, j.ID, prompt, prior)
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case err != nil && ctx.Err() != nil:
		j.state = AgentStopped
	case err != nil:
		j.state, j.err = AgentFailed, err.Error()
	default:
		j.state, j.text, j.history = AgentDone, res.Text, history
	}
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

// Snapshots 全量只读快照（e2e 等待终态、后续 UI 面同源）。
func (a *AgentJobs) Snapshots() []AgentJobView {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AgentJobView, 0, len(a.jobs))
	for _, j := range a.jobs {
		out = append(out, j.Snapshot())
	}
	return out
}

// CloseAll 全停（会话删除/服务关闭）；注册项保留可读（收割已完成的）。
func (a *AgentJobs) CloseAll() { a.end() }
