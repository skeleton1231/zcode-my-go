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
	store  *Store
	runner Runner
	now    func() time.Time // 注入时钟

	mu      sync.Mutex
	running map[string]bool
	cancel  context.CancelFunc
	stopped chan struct{}
	// inflight 追踪派发的 fire goroutine（issue #134：Stop 曾只等 tick
	// 循环——在飞的 run 结束后 record 写盘晚于关闭，最后一条记录丢失，
	// 下个启动周期双触发；测试侧与 t.TempDir 清理竞争致全套件抖动）。
	inflight sync.WaitGroup
}

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
		now := s.now()
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

// SetClock injects the time source（测试注入固定时钟）.
func (s *Scheduler) SetClock(now func() time.Time) { s.now = now }

// FireDue runs one minute-tick synchronously（e2e：不真实等一分钟）.
func (s *Scheduler) FireDue(ctx context.Context) { s.tick(ctx) }

// Stop ends the loop and waits for it.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.stopped
		// 排空在飞的 fire（issue #134）：record 落盘完成后才返回，调用
		// 方（应用退出、测试清理）随即关 store/删 TempDir 不再竞争。
		s.inflight.Wait()
	}
}

// tick re-reads the store（热重载）and fires everything due.
func (s *Scheduler) tick(ctx context.Context) {
	jobs, err := s.store.List()
	if err != nil {
		return // 文件损坏时保守跳过本轮，下一分钟重试
	}
	now := s.now().Truncate(time.Minute)
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
	s.running[job.ID] = true
	s.mu.Unlock()

	started := s.now()
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, job.ID)
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
func (s *Scheduler) RunNow(ctx context.Context, job Job) (string, error) {
	summary, err := s.runner.Run(ctx, job)
	status := "ok"
	if err != nil {
		status = "error: " + err.Error()
	}
	s.recordWithTime(job.ID, status, summary, s.now())
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
