// Package workflow persists workflow definitions and run records（ZCode
// saved-workflows / workflow-runs 的落地，spec 规则 48）：定义存
// dataDir/workflows/<名>.json，运行记录存 dataDir/workflow-runs/<id>.json
// （开跑即落盘 running，终态更新——崩溃后未完记录可续跑）。
package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// 目录与封顶常量。
const (
	defsDir      = "workflows"
	runsDir      = "workflow-runs"
	maxSavedDefs = 200
	maxRunsKept  = 50
)

// Definition 是一份已保存的工作流定义（步骤与说明，可反复执行）。
type Definition struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Steps       []agent.WorkflowStep `json:"steps"`
	SavedAt     time.Time            `json:"savedAt"`
}

// RunStep 是运行记录里的一步（含结果与完成标记）。
type RunStep struct {
	Index       int      `json:"index"`
	Name        string   `json:"name,omitempty"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	After       []string `json:"after,omitempty"` // 临场编排的重建需要
	Text        string   `json:"text"`
	Failed      bool     `json:"failed"`
	// Done 标记该步已成功完成（续跑据此跳过；失败步骤不置 Done）。
	Done bool `json:"done"`
}

// StepsOf 把记录里的步骤表还原成可执行形态（续跑临场编排时定义不在
// 存储里，从记录重建）。
func (r Run) StepsOf() []agent.WorkflowStep {
	out := make([]agent.WorkflowStep, 0, len(r.Steps))
	for _, rs := range r.Steps {
		out = append(out, agent.WorkflowStep{Description: rs.Description,
			Kind: rs.Kind, Name: rs.Name, After: rs.After})
	}
	return out
}

// Run 是一次执行的记录。
type Run struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"` // 命名工作流名；临场编排为空
	Workspace string    `json:"workspace"`
	Status    string    `json:"status"` // running | completed | failed
	Steps     []RunStep `json:"steps"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
}

// Store 是定义与运行记录的持久层（并发安全：保存/记录来自会话回
// 合，CLI 与续跑并发读）。
type Store struct {
	mu   sync.Mutex
	root string // dataDir
}

// NewStore 构建存储。
func NewStore(dataDir string) *Store { return &Store{root: dataDir} }

// ValidName 校验工作流名：小写字母/数字/连字符，1..64——文件名安全。
func ValidName(name string) error {
	if n := len(name); n < 1 || n > 64 {
		return fmt.Errorf("workflow 名长度须在 1..64（%d）", n)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Errorf("workflow 名只允许小写字母/数字/连字符：%q", name)
		}
	}
	return nil
}

func (s *Store) defsPath(name string) string {
	return filepath.Join(s.root, defsDir, name+".json")
}

func (s *Store) runsPath(id string) string {
	return filepath.Join(s.root, runsDir, id+".json")
}

// Save 持久化一份定义（原子写：临时文件 + rename；同号覆盖）。
func (s *Store) Save(def Definition) error {
	if err := ValidName(def.Name); err != nil {
		return err
	}
	if len(def.Steps) == 0 {
		return fmt.Errorf("workflow %s 没有步骤", def.Name)
	}
	if def.SavedAt.IsZero() {
		def.SavedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(s.root, defsDir), 0o700); err != nil {
		return err
	}
	return atomicWriteJSON(s.defsPath(def.Name), def)
}

// Get 读取一份定义。
func (s *Store) Get(name string) (Definition, error) {
	if err := ValidName(name); err != nil {
		return Definition{}, err
	}
	data, err := os.ReadFile(s.defsPath(name))
	if err != nil {
		return Definition{}, fmt.Errorf("workflow %s 不存在（zgo workflow list 查看）", name)
	}
	var def Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return Definition{}, fmt.Errorf("workflow %s 损坏：%v", name, err)
	}
	return def, nil
}

// List 列出全部定义（按名排序）。
func (s *Store) List() []Definition {
	entries, err := os.ReadDir(filepath.Join(s.root, defsDir))
	if err != nil {
		return nil
	}
	var out []Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, defsDir, e.Name()))
		if err != nil {
			continue
		}
		var def Definition
		if json.Unmarshal(data, &def) == nil && def.Name != "" {
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NewRunID 生成运行 ID：时间戳 + 同拍序号（Windows 时钟粒度教训——
// 会话 ID 撞号 issue 同源）。
var runSeq int

// StartRun 开跑即落盘 running 记录（崩溃可续跑的锚点）。
func (s *Store) StartRun(name, workspace string, steps []agent.WorkflowStep) (*Run, error) {
	s.mu.Lock()
	runSeq++
	seq := runSeq
	s.mu.Unlock()
	run := &Run{
		ID:        time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", seq),
		Name:      name,
		Workspace: workspace,
		Status:    "running",
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	for i, st := range steps {
		run.Steps = append(run.Steps, RunStep{Index: i, Name: st.Name,
			Kind: st.Kind, Description: st.Description, After: st.After})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(s.root, runsDir), 0o700); err != nil {
		return nil, err
	}
	if err := atomicWriteJSON(s.runsPath(run.ID), *run); err != nil {
		return nil, err
	}
	s.pruneRunsLocked() // 开跑即裁：崩溃的 running 记录同样会累积
	return run, nil
}

// FinishRun 更新终态（结果回填、状态与错误）。results 按源序回填：
// 顺序模式返回前缀（失败即止），DAG 模式返回全长（未派发为空值）——
// 都与 StartRun 记录的下标一一对位。
func (s *Store) FinishRun(run *Run, results []agent.StepResult, status, runErr string) error {
	run.Status = status
	run.Error = runErr
	run.UpdatedAt = time.Now()
	for i := range run.Steps {
		run.Steps[i].Done, run.Steps[i].Failed, run.Steps[i].Text = false, false, ""
	}
	for i, res := range results {
		if i >= len(run.Steps) {
			break
		}
		run.Steps[i].Text = res.Text
		run.Steps[i].Failed = res.Failed
		run.Steps[i].Done = !res.Failed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := atomicWriteJSON(s.runsPath(run.ID), *run); err != nil {
		return err
	}
	s.pruneRunsLocked()
	return nil
}

// GetRun 读取一条运行记录。
func (s *Store) GetRun(id string) (Run, error) {
	if strings.ContainsAny(id, `/\.`) || strings.ContainsFunc(id, unicode.IsSpace) {
		return Run{}, fmt.Errorf("无效运行 ID %q", id)
	}
	data, err := os.ReadFile(s.runsPath(id))
	if err != nil {
		return Run{}, fmt.Errorf("运行 %s 不存在（zgo workflow runs 查看）", id)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return Run{}, fmt.Errorf("运行 %s 损坏：%v", id, err)
	}
	return run, nil
}

// ListRuns 列出运行记录（新在前，limit ≤0 取全部）。
func (s *Store) ListRuns(limit int) []Run {
	entries, err := os.ReadDir(filepath.Join(s.root, runsDir))
	if err != nil {
		return nil
	}
	var out []Run
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, runsDir, e.Name()))
		if err != nil {
			continue
		}
		var run Run
		if json.Unmarshal(data, &run) == nil && run.ID != "" {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// PrefillOf 把运行记录里已完成的步骤转成续跑注入（下标 → 结果）。
func (r Run) PrefillOf(steps []agent.WorkflowStep) map[int]agent.StepResult {
	prefill := map[int]agent.StepResult{}
	for _, rs := range r.Steps {
		if !rs.Done || rs.Index >= len(steps) {
			continue
		}
		want := steps[rs.Index]
		if want.Description != rs.Description || want.Kind != rs.Kind {
			continue // 定义已漂移：以定义为准，重跑该步
		}
		prefill[rs.Index] = agent.StepResult{Step: want, Text: rs.Text}
	}
	return prefill
}

// pruneRunsLocked 把运行记录裁到保留窗口（最旧先删；锁内调用）。
func (s *Store) pruneRunsLocked() {
	entries, err := os.ReadDir(filepath.Join(s.root, runsDir))
	if err != nil || len(entries) <= maxRunsKept {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // ID 时间戳前缀：字典序即时间序
	for _, n := range names[:len(names)-maxRunsKept] {
		_ = os.Remove(filepath.Join(s.root, runsDir, n))
	}
}

// atomicWriteJSON 原子落盘（临时文件 + rename，防半写记录）。
func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
