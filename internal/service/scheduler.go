package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/schedule"
)

// scheduleRunner runs one job headless（ZCode automation 的运行时）：每轮
// 新建会话、一次完整 Chat（权限门照常——无 asker 的进程里改动类工具按
// 既有语义被拒，定时任务因此天然只读安全），末条 assistant 文本即摘要。
type scheduleRunner struct{ s *AgentService }

func (r scheduleRunner) Run(ctx context.Context, job schedule.Job) (string, error) {
	info, err := r.s.NewSession(job.Workspace)
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	var buf, last strings.Builder
	emit := func(ev agent.Event) {
		switch ev.Kind {
		case agent.EvMessageStart:
			buf.Reset()
		case agent.EvTextDelta:
			buf.WriteString(ev.Text)
		case agent.EvMessageEnd:
			if buf.Len() > 0 {
				last.Reset()
				last.WriteString(buf.String())
			}
		}
	}
	if err := r.s.Chat(info.ID, job.Prompt, emit); err != nil {
		return last.String(), err
	}
	return last.String(), nil
}

// JobStore returns the automation job file's store（与 CLI 共用 jobs.json）.
func (s *AgentService) JobStore() *schedule.Store {
	return schedule.NewStore(filepath.Dir(s.path))
}

// StartScheduler boots the minute-tick scheduler on this service (the
// desktop shell calls it at app ready; CLI `zgo cron run` runs jobs
// directly instead). Double start is a no-op.
func (s *AgentService) StartScheduler() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sched != nil {
		return
	}
	sched := schedule.NewScheduler(s.JobStore(), scheduleRunner{s})
	s.sched = sched
	sched.Start()
}

// Scheduler returns the running scheduler, or nil before StartScheduler
// （面板/测试拿到它可查下次到期、注入时钟）.
func (s *AgentService) Scheduler() *schedule.Scheduler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sched
}

// StopScheduler ends the scheduler and waits for the loop.
func (s *AgentService) StopScheduler() {
	s.mu.Lock()
	sched := s.sched
	s.sched = nil
	s.mu.Unlock()
	if sched != nil {
		sched.Stop()
	}
}

// Jobs lists the automation jobs（面板数据源）.
func (s *AgentService) Jobs() ([]schedule.Job, error) { return s.JobStore().List() }

// UpsertJob adds or replaces a job, validating the schedule up front.
func (s *AgentService) UpsertJob(j schedule.Job) error {
	if _, err := schedule.ParseCron(j.Schedule); err != nil {
		return err
	}
	if strings.TrimSpace(j.Prompt) == "" || strings.TrimSpace(j.Workspace) == "" {
		return fmt.Errorf("任务需要提示与工作区")
	}
	return s.JobStore().Upsert(j)
}

// RemoveJob drops a job by ID.
func (s *AgentService) RemoveJob(id string) (bool, error) { return s.JobStore().Remove(id) }

// ToggleJob flips a job's enabled switch.
func (s *AgentService) ToggleJob(id string, on bool) (bool, error) {
	return s.JobStore().SetEnabled(id, on)
}

// RunJobNow fires one job immediately (zgo cron run) and returns its
// summary.
func (s *AgentService) RunJobNow(ctx context.Context, id string) (string, error) {
	jobs, err := s.JobStore().List()
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if j.ID == id {
			return schedule.NewScheduler(s.JobStore(), scheduleRunner{s}).
				RunNow(ctx, j)
		}
	}
	return "", fmt.Errorf("no job %s", id)
}
