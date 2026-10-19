package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SummaryCap bounds a run's recorded answer（工具输出可能巨大，摘要必须截断）.
const SummaryCap = 2000

// Job is one scheduled prompt (ZCode's automation task).
type Job struct {
	ID          string    `json:"id"`
	Schedule    string    `json:"schedule"`
	Prompt      string    `json:"prompt"`
	Workspace   string    `json:"workspace"`
	Model       string    `json:"model,omitempty"`
	Window      string    `json:"window,omitempty"` // HH:MM-HH:MM（本地时区，可跨午夜）；空即不限
	// At 是创建时的自然语言原文（spec 规则 95）：只作回显，调度仍以
	// Schedule（五字段 cron）为准。
	At          string    `json:"at,omitempty"`
	Enabled     bool      `json:"enabled"`
	LastRun     time.Time `json:"lastRun,omitempty"`
	LastStatus  string    `json:"lastStatus,omitempty"`
	LastSummary string    `json:"lastSummary,omitempty"`
}

// Runner executes one job run and returns the answer's summary.
type Runner interface {
	Run(ctx context.Context, job Job) (string, error)
}

// Store keeps the jobs file. The scheduler re-reads it every minute so CLI
// edits take effect without a restart; saves are atomic (temp + rename).
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore points at <dir>/jobs.json.
func NewStore(dir string) *Store { return &Store{path: filepath.Join(dir, "jobs.json")} }

// List reads the jobs (missing file = empty, malformed file = error).
// 持存储锁（issue #117）：读-改-写调用方（recordWithTime/Upsert）曾各
// 自 List→改→Save 无锁竞争——并发完成的 job 互相覆盖，丢 LastRun 的
// job 下个 tick 双触发。锁保护整个 RMW 序列，不只是单次读写。
func (st *Store) List() ([]Job, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.listLocked()
}

// listLocked 是 List 的无锁体（已在锁内的 RMW 序列共用）。
func (st *Store) listLocked() ([]Job, error) {
	data, err := os.ReadFile(st.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading jobs: %w", err)
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parsing jobs: %w", err)
	}
	return jobs, nil
}

// Save writes the jobs atomically.
func (st *Store) Save(jobs []Job) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.saveLocked(jobs)
}

// saveLocked 是 Save 的无锁体。
func (st *Store) saveLocked(jobs []Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// Upsert adds or replaces a job by ID.
// Update 在存储锁内原子地读-改-写（issue #117：各方法单独 List→改→
// Save 的窗口里，并发完成的 record 会互相覆盖、面板编辑会被回滚——
// 丢 LastRun 的 job 下个 tick 双触发）。mutate 返回 nil 表示无改动。
func (st *Store) Update(mutate func(jobs []Job) []Job) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	jobs, err := st.listLocked()
	if err != nil {
		return err
	}
	if out := mutate(jobs); out != nil {
		return st.saveLocked(out)
	}
	return nil
}

// UpdateResult 是带回执的 Update（found 语义）。
func (st *Store) UpdateResult(mutate func(jobs []Job) ([]Job, bool)) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	jobs, err := st.listLocked()
	if err != nil {
		return false, err
	}
	out, found := mutate(jobs)
	if found {
		return true, st.saveLocked(out)
	}
	return false, nil
}

func (st *Store) Upsert(j Job) error {
	return st.Update(func(jobs []Job) []Job {
		for i := range jobs {
			if jobs[i].ID == j.ID {
				jobs[i] = j
				return jobs
			}
		}
		return append(jobs, j)
	})
}

// Remove drops a job by ID, reporting whether it existed.
func (st *Store) Remove(id string) (bool, error) {
	return st.UpdateResult(func(jobs []Job) ([]Job, bool) {
		out := jobs[:0]
		found := false
		for _, j := range jobs {
			if j.ID == id {
				found = true
				continue
			}
			out = append(out, j)
		}
		if !found {
			return nil, false
		}
		return out, true
	})
}

// SetEnabled flips a job's switch by ID.
func (st *Store) SetEnabled(id string, on bool) (bool, error) {
	return st.UpdateResult(func(jobs []Job) ([]Job, bool) {
		found := false
		for i := range jobs {
			if jobs[i].ID == id {
				jobs[i].Enabled = on
				found = true
			}
		}
		if !found {
			return nil, false
		}
		return jobs, true
	})
}

// Scheduler fires due jobs on a minute tick. Results are recorded back to
// the store; one job never overlaps itself (the run is skipped and noted).
type Scheduler struct {
	store   *Store
	runner  Runner
	now     func() time.Time // 注入时钟（经 clockMu 与循环 goroutine 互斥）
	clockMu sync.Mutex

	mu      sync.Mutex
	running map[string]bool
	// active 是在飞 run 的全局计数（spec 规则 78：并发上限）。
	active  int
	cancel  context.CancelFunc
	stopped chan struct{}
	// inflight 追踪派发的 fire goroutine（issue #134：Stop 曾只等 tick
	// 循环——在飞的 run 结束后 record 写盘晚于关闭，最后一条记录丢失，
	// 下个启动周期双触发；测试侧与 t.TempDir 清理竞争致全套件抖动）。
	inflight sync.WaitGroup
}

// maxConcurrentRuns 是定时任务的全局并发上限（spec 规则 78）：每个
// run 是一次完整 agent 会话——同时跑 N 个例程任务既烧 token 也抢
// provider 配额。超出的到期任务本轮不启动（LastRun 不动——任务保持
// 到期，下一分钟 tick 自然重试，排队而非跳过）。
const maxConcurrentRuns = 2

// NewScheduler wires a store and runner.
func NewScheduler(store *Store, runner Runner) *Scheduler {
	return &Scheduler{store: store, runner: runner, now: time.Now,
		running: map[string]bool{}, stopped: make(chan struct{})}
}

// Start launches the minute loop on its own goroutine; Stop ends it.
func (s *Scheduler) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		defer close(s.stopped)
		// 对齐全分钟边界再开始每分钟滴答。
		now := s.clock()
		until := now.Truncate(time.Minute).Add(time.Minute).Sub(now)
		select {
		case <-time.After(until):
		case <-ctx.Done():
			return
		}
		s.tick(ctx)
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.tick(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// SetClock injects the time source（测试注入固定时钟）。Start 的循环
// goroutine 与本调用并发读写 s.now——互斥保护（-race 在 CI 上逮过）。
func (s *Scheduler) SetClock(now func() time.Time) {
	s.clockMu.Lock()
	s.now = now
	s.clockMu.Unlock()
}

// clock 读取当前时钟（持锁——与 SetClock 互斥）。
func (s *Scheduler) clock() time.Time {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	return s.now()
}

// FireDue runs one minute-tick synchronously（e2e：不真实等一分钟）.
func (s *Scheduler) FireDue(ctx context.Context) { s.tick(ctx) }

// Stop ends the loop and waits for it.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.stopped
	}
	// 排空无条件执行（issue #134 的 Windows 补遗）：手动 tick（未
	// Start）的调用方同样要等 record 落盘完成再删数据目录——CI 曾因
	// 清理竞态报「directory is not empty」（Windows TempDir RemoveAll
	// 撞上 fire goroutine 的 LastRun 写回）。
	s.inflight.Wait()
}

// tick re-reads the store（热重载）and fires everything due.
func (s *Scheduler) tick(ctx context.Context) {
	jobs, err := s.store.List()
	if err != nil {
		return // 文件损坏时保守跳过本轮，下一分钟重试
	}
	now := s.clock().Truncate(time.Minute)
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		sched, err := ParseCron(job.Schedule)
		if err != nil {
			continue
		}
		anchor := job.LastRun
		if anchor.IsZero() {
			anchor = now.Add(-time.Minute)
		}
		next := sched.Next(anchor)
		if next.IsZero() || next.After(now) {
			continue
		}
		// 时间窗（spec 规则 64，pi off-peak 的形态）：窗口外不 fire 也
		// 不动 LastRun——任务保持到期，窗口一开的下一个 tick 即补跑。
		if !job.InWindow(now) {
			continue
		}
		s.fire(ctx, job)
	}
}

// fire runs one job off-thread, guarding against overlap and recording
// the outcome.
func (s *Scheduler) fire(ctx context.Context, job Job) {
	s.mu.Lock()
	if s.running[job.ID] {
		s.mu.Unlock()
		s.record(job.ID, "skipped: 上一轮仍在运行", "")
		return
	}
	// 全局并发上限（spec 规则 78）：满员即不启动——LastRun 不动，任务
	// 保持到期，下一 tick 自然重试（排队语义，不记 skipped 噪音）。
	if s.active >= maxConcurrentRuns {
		s.mu.Unlock()
		return
	}
	s.running[job.ID] = true
	s.active++
	s.mu.Unlock()

	started := s.clock()
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, job.ID)
			s.active--
			s.mu.Unlock()
		}()
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		summary, err := s.runner.Run(runCtx, job)
		status := "ok"
		if err != nil {
			status = "error: " + err.Error()
		}
		s.recordWithTime(job.ID, status, summary, started)
	}()
}

// RunNow forces one immediate run (zgo cron run), waiting for it.
// 互斥语义与 fire 同源（spec 规则 78）：该任务已在跑（无论来自 tick
// 还是另一次手动）即拒绝并说明——手动触发不再绕过 single-flight。
func (s *Scheduler) RunNow(ctx context.Context, job Job) (string, error) {
	s.mu.Lock()
	if s.running[job.ID] {
		s.mu.Unlock()
		return "", fmt.Errorf("上一轮仍在运行，稍后再试")
	}
	s.running[job.ID] = true
	s.active++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, job.ID)
		s.active--
		s.mu.Unlock()
	}()
	summary, err := s.runner.Run(ctx, job)
	status := "ok"
	if err != nil {
		status = "error: " + err.Error()
	}
	s.recordWithTime(job.ID, status, summary, s.clock())
	return summary, err
}

func (s *Scheduler) record(id, status, summary string) {
	s.recordWithTime(id, status, summary, s.now())
}

func (s *Scheduler) recordWithTime(id, status, summary string, at time.Time) {
	// 按 rune 截断（issue #117：字节截断让中文摘要在上限处乱码——同类
	// 第 4 例，#12/#40/#99 均已 rune 安全化）。
	if r := []rune(summary); len(r) > SummaryCap {
		summary = string(r[:SummaryCap]) + "…"
	}
	trimmed := strings.TrimSpace(summary)
	_ = s.store.Update(func(jobs []Job) []Job {
		for i := range jobs {
			if jobs[i].ID == id {
				jobs[i].LastRun = at.UTC()
				jobs[i].LastStatus = status
				jobs[i].LastSummary = trimmed
			}
		}
		return jobs
	})
}

// NextDue reports a job's next fire time (zero when it never matches).
func NextDue(job Job, after time.Time) time.Time {
	sched, err := ParseCron(job.Schedule)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(after)
}

// SortedJobs lists jobs by ID for stable display.
func SortedJobs(jobs []Job) []Job {
	out := append([]Job(nil), jobs...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
