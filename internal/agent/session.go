package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/skeleton1231/zcode-my-go/internal/agent/compact"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// RunState of a session.
type RunState string

// Run states.
const (
	StateIdle    RunState = "idle"
	StateRunning RunState = "running"
)

// SessionInfo is the listable projection of a session.
type SessionInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Workspace string    `json:"workspace"`
	UpdatedAt time.Time `json:"updatedAt"`
	Running   bool      `json:"running"`
}

// Session is a conversation: a transcript persisted as JSONL plus run state.
// The transcript's only writer is Append (the loop appends through it);
// nothing rewrites history.
type Session struct {
	mu              sync.Mutex
	info            SessionInfo
	messages        []ai.Message
	path            string
	metaTitle       string // title as last written to disk
	cancel          context.CancelFunc
	permissions     *permission.Service // session-scoped rules die with it
	compactFailures int
	usageTotal      ai.Usage     // cumulative tokens, persisted per run
	steering        []ai.Message // queued while a run is in flight

	// wiring, installed by the manager
	provider   func() ai.Provider
	model      func() string
	thinking   func() ai.ThinkingLevel
	hooks      Hooks
	buildTools func(workspace, sessionID string) []Tool
	system     func(workspace string) string
	ask        func(AskRequest) AskResponse
}

// Info returns the session's projection.
func (s *Session) Info() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.info
	info.Running = s.cancel != nil
	return info
}

// Messages returns a copy of the transcript.
func (s *Session) Messages() []ai.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ai.Message(nil), s.messages...)
}

// Append adds a message to the transcript and persists it. It is the single
// write path of the history.
func (s *Session) Append(m ai.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m = truncateMessage(m) // 唯一写入路径：内存与磁盘一致（issue #10）
	s.messages = append(s.messages, m)
	s.info.UpdatedAt = time.Now()
	if s.info.Title == "" && m.Role == ai.RoleUser {
		s.info.Title = titleFrom(m.Text())
	}
	if err := s.writeLine(m); err != nil {
		return fmt.Errorf("persisting message: %w", err)
	}
	// The title changes once, with the first user message, when the file is
	// still tiny — rewrite then and only then.
	if s.info.Title != s.metaTitle {
		return s.writeMetaLocked()
	}
	return nil
}

// Chat appends the user message and runs the loop, streaming events through
// emit. It returns an error only for misuse (empty message, already
// running); provider and tool failures arrive as events.
func (s *Session) Chat(text string, emit func(Event)) error {
	return s.ChatMedia(text, nil, emit)
}

// userMessage assembles the user turn: the text plus any image blocks
// （多模态输入，顺序与附件一致）.
func userMessage(text string, media []ai.Block) ai.Message {
	blocks := []ai.Block{{Kind: ai.BlockText, Text: text}}
	return ai.Message{Role: ai.RoleUser, Blocks: append(blocks, media...)}
}

// ChatMedia is Chat carrying image attachments (BlockImage blocks are
// appended after the text and persist with the transcript).
func (s *Session) ChatMedia(text string, media []ai.Block, emit func(Event)) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("empty message")
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("a run is already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	id := s.info.ID
	workspace := s.info.Workspace
	s.mu.Unlock()

	// Clear the running flag whatever happens, including a panic in the loop.
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		// 运行结束即清空插话队列：abort 残留的插话不得静默泄入下一次
		// 运行的首个回合（abort 语义 = 取消一切排队内容）。
		s.steering = nil
		s.mu.Unlock()
		cancel()
	}()

	// 微压缩先行（ZCode microcompact）：无 provider 成本，修剪保留窗口外
	// 的长工具输出；不足以达标时再走全量摘要。
	s.mu.Lock()
	if pruned, changed := compact.Microcompact(s.messages, compact.DefaultKeepTail); changed {
		s.messages = pruned
	}
	s.mu.Unlock()
	modelInfo := ai.LookupModel(s.model())
	s.autoCompact(ctx, func(ev Event) { ev.SessionID = id; emit(ev) }, modelInfo.ContextWindow)

	if err := s.Append(userMessage(text, media)); err != nil {
		return err
	}
	s.run(ctx, workspace, id, modelInfo, emit)
	return nil
}

// run drives the loop for one turn sequence (Chat and Retry share it).
func (s *Session) run(ctx context.Context, workspace, id string, modelInfo ai.ModelInfo, emit func(Event)) {
	thinking := ai.ThinkingOff
	if s.thinking != nil {
		thinking = s.thinking()
	}
	loop := NewLoop(LoopConfig{
		Provider:  s.provider(),
		Model:     s.model(),
		System:    s.system(workspace),
		Tools:     s.buildTools(workspace, id),
		MaxTokens: modelInfo.MaxOutput,
		MaxTurns:  DefaultMaxTurns,
		Thinking:  thinking,
		// 并行执行并发安全工具（pi 的 overlap 语义）：串行工具（ask、
		// todo）强制整批串行，权限确认经 askMu 串行化——安全前提下
		// 让批处理真正提速。
		ToolExecution: ParallelExecution,
		Hooks:         s.hooks,
		History:       s.Messages,
		SessionID:     id,
		Permissions:   s.permissions,
		Ask:           s.ask,
		Steering:      s.drainSteering,
	}, func(ev Event) {
		ev.SessionID = id
		if ev.Kind == EvAgentEnd && ev.Usage != (ai.Usage{}) {
			// agent_end 携带本次运行的累计值：落账。必须同步（issue #47）：
			// 裸 goroutine 在 Chat 返回后进程即退时丢失本轮记录，连续
			// 运行还可能乱序写盘让累计值回退。emit 本就在 loop 的
			// goroutine 上，addUsage 只是一次加锁 append，无需再包一层。
			s.addUsage(ev.Usage)
		}
		emit(ev)
	})
	loop.Run(ctx, s.Append)
}

// Retry drops everything from the last user message onward and regenerates
// the answer (ZCode's regenerate): the transcript truncation is persisted
// as a regenerate record and replayed on load, keeping the file
// append-only.
func (s *Session) Retry(emit func(Event)) error {
	last := s.lastUserIndex()
	if last < 0 {
		return errors.New("nothing to regenerate")
	}
	return s.rewindAndSend(last, s.messages[last].Text(), emit)
}

// EditAndResend replaces the last user message with new text and reruns
// (ZCode's edit-message): same truncation semantics as Retry, persisted
// the same way.
func (s *Session) EditAndResend(text string, emit func(Event)) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("empty message")
	}
	last := s.lastUserIndex()
	if last < 0 {
		return errors.New("nothing to edit")
	}
	return s.rewindAndSend(last, text, emit)
}

// lastUserIndex is the index of the last user message, or -1.
func (s *Session) lastUserIndex() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := -1
	for i, m := range s.messages {
		if m.Role == ai.RoleUser {
			last = i
		}
	}
	return last
}

// rewindAndSend truncates the transcript before the given user message,
// persists the truncation as a regenerate record, and runs a new turn with
// the given text (Retry and EditAndResend share it).
func (s *Session) rewindAndSend(userIndex int, text string, emit func(Event)) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("a run is already in progress")
	}
	s.messages = s.messages[:userIndex]
	workspace := s.info.Workspace
	modelInfo := ai.LookupModel(s.model())
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	id := s.info.ID
	rec, err := json.Marshal(regenerateRecord{Type: "regenerate", Kept: userIndex})
	if err == nil {
		err = appendLine(s.path, rec)
	}
	s.mu.Unlock()
	if err != nil {
		cancel()
		return fmt.Errorf("persisting regenerate: %w", err)
	}

	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}()
	if err := s.Append(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}}); err != nil {
		return err
	}
	s.run(ctx, workspace, id, modelInfo, emit)
	return nil
}

// SetPlanMode switches the session's collaboration mode.
// Rewind truncates the transcript before the given user message and
// persists the truncation like rewindAndSend does, without starting a new
// turn — the conversation half of the workspace rollback (the service
// restores the checkpoint before calling this).
func (s *Session) Rewind(userIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("a run is already in progress")
	}
	if userIndex < 0 || userIndex >= len(s.messages) ||
		s.messages[userIndex].Role != ai.RoleUser {
		return fmt.Errorf("message %d is not a user turn", userIndex)
	}
	s.messages = s.messages[:userIndex]
	rec, err := json.Marshal(regenerateRecord{Type: "regenerate", Kept: userIndex})
	if err != nil {
		return err
	}
	return appendLine(s.path, rec)
}

// LastUserIndex is the transcript index of the last user message, or -1.
func LastUserIndex(msgs []ai.Message) int {
	last := -1
	for i, m := range msgs {
		if m.Role == ai.RoleUser {
			last = i
		}
	}
	return last
}

func (s *Session) SetPlanMode(on bool) {
	mode := permission.ModeNormal
	if on {
		mode = permission.ModePlan
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permissions != nil {
		s.permissions.SetMode(mode)
	}
}

// Steer queues a user message while a run is in flight (pi's steering):
// the loop injects it before its next turn; idle sessions reject — send
// instead.
func (s *Session) Steer(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("empty message")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return errors.New("no run in progress; send the message instead")
	}
	s.steering = append(s.steering, ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}})
	return nil
}

// drainSteering hands the queued messages to the loop and forgets them.
func (s *Session) drainSteering() []ai.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steering) == 0 {
		return nil
	}
	out := s.steering
	s.steering = nil
	return out
}

// Rename sets the session's title (ZCode's rename), rewriting the meta
// line like Append's first-title write does. 磁盘写失败时报错并回滚内存
// 标题（issue #52：不吞错，内存与磁盘不得静默分裂）。
func (s *Session) Rename(title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:79]) + "…"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if title == "" || title == s.info.Title {
		return nil
	}
	old := s.info.Title
	s.info.Title = title
	if err := s.writeMetaLocked(); err != nil {
		s.info.Title = old
		return fmt.Errorf("persisting rename: %w", err)
	}
	return nil
}

// UsageTotal returns the session's cumulative token usage.
func (s *Session) UsageTotal() ai.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageTotal
}

// addUsage merges a run's totals and persists the cumulative value as one
// usage record per run (ZCode's usage-totals).
func (s *Session) addUsage(u ai.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usageTotal = s.usageTotal.Add(u)
	rec, err := json.Marshal(usageRecord{Type: "usage", Usage: s.usageTotal})
	if err != nil {
		return
	}
	if err := appendLine(s.path, rec); err != nil {
		// 不吞错（issue #52）：usage 记录落盘失败记日志，内存累计继续
		// 前进（重放取磁盘末条，缺失一条只会少记，不会回退）。
		log.Printf("session %s: persisting usage: %v", s.info.ID, err)
	}
}

// PlanMode reports whether the session is in plan mode.
func (s *Session) PlanMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.permissions != nil && s.permissions.Mode() == permission.ModePlan
}

// Abort cancels the running loop, if any.
func (s *Session) Abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// summarize makes a session title from the first user message.
func titleFrom(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > 40 {
		return string([]rune(text)[:39]) + "…"
	}
	return text
}

// --- persistence ---------------------------------------------------------

// jsonl record kinds.
const (
	recordMeta    = "meta"
	recordMessage = "message"
)

type metaRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Workspace string    `json:"workspace"`
	CreatedAt time.Time `json:"createdAt"`
}

type messageRecord struct {
	Type    string     `json:"type"`
	Message ai.Message `json:"message"`
}

// usageRecord persists the cumulative usage after each run.
type usageRecord struct {
	Type  string   `json:"type"`
	Usage ai.Usage `json:"usage"`
}

// regenerateRecord marks a retry: messages beyond Kept were dropped.
type regenerateRecord struct {
	Type string `json:"type"`
	Kept int    `json:"kept"`
}

// 会话持久化的大小上限（issue #10）：maxBlockText 是单个文本块的落盘
// 上限——用户粘贴超大日志曾产生 >8MB 的 JSONL 行，超过读取上限后整个
// 会话文件加载失败，会话从列表静默消失。maxLoadLine 是读取侧对单行的
// 防御性上限，超限行按损坏跳过（兼容历史文件）。
const (
	maxBlockText = 1 << 20 // 1MB per text block
	maxLoadLine  = 16 << 20
)

// truncateMessage 把超大文本块截断到 maxBlockText（退到 rune 边界）并
// 追加标记。内存与磁盘共用同一份数据，重放保持一致。工具输出已在上游
// 截断到 30KB，这里主要防用户粘贴。
func truncateMessage(m ai.Message) ai.Message {
	capped := false
	blocks := make([]ai.Block, len(m.Blocks))
	for i, b := range m.Blocks {
		if b.Kind == ai.BlockText && len(b.Text) > maxBlockText {
			cut := maxBlockText
			for cut > 0 && !utf8.RuneStart(b.Text[cut]) {
				cut--
			}
			b.Text = b.Text[:cut] + "…[已截断]"
			capped = true
		}
		blocks[i] = b
	}
	if !capped {
		return m
	}
	m.Blocks = blocks
	return m
}

// writeLine appends one message record. Called with s.mu held.
func (s *Session) writeLine(m ai.Message) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(messageRecord{Type: recordMessage, Message: m})
}

// writeMetaLocked rewrites the header line: meta is always line one, so the
// rewrite happens only while the file holds no history yet. Called with
// s.mu held.
func (s *Session) writeMetaLocked() error {
	rec, err := json.Marshal(metaRecord{
		Type: recordMeta, ID: s.info.ID, Title: s.info.Title,
		Workspace: s.info.Workspace, CreatedAt: s.info.UpdatedAt,
	})
	if err != nil {
		return err
	}
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		lines = append([]string{string(rec) + "\n"}, lines...)
	} else {
		lines[0] = string(rec) + "\n"
	}
	if err := os.WriteFile(s.path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		return err
	}
	s.metaTitle = s.info.Title
	return nil
}

// loadSessionSafe 兜底 loadSession 的 panic（issue #9：损坏的会话文件曾
// 让启动 brick）。recover 后按加载失败处理，交给调用方跳过。
func loadSessionSafe(path string) (s *Session, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("session %s: panic on load: %v", path, r)
			err = fmt.Errorf("panic on load: %v", r)
		}
	}()
	return loadSession(path)
}

// loadSession reads a session file. A missing meta record is fatal; damaged
// message lines are skipped, not fatal.
func loadSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := &Session{path: path}
	// bufio.Scanner 的 token 上限会让超长行拖垮整个文件（issue #10）；
	// 改用 Reader 逐行读，超限行按损坏跳过，其余行照常重放。
	r := bufio.NewReader(f)
	for {
		raw, rerr := r.ReadString('\n')
		line := strings.TrimSpace(raw)
		if line != "" && len(line) <= maxLoadLine {
			var kind struct{ Type string }
			if err := json.Unmarshal([]byte(line), &kind); err == nil {
				switch kind.Type {
				case recordMeta:
					var rec metaRecord
					if json.Unmarshal([]byte(line), &rec) == nil {
						s.info.ID = rec.ID
						s.info.Title = rec.Title
						s.info.Workspace = rec.Workspace
						s.metaTitle = rec.Title
					}
				case recordMessage:
					var rec messageRecord
					if json.Unmarshal([]byte(line), &rec) == nil {
						s.messages = append(s.messages, rec.Message)
					}
				case "usage":
					var rec usageRecord
					if json.Unmarshal([]byte(line), &rec) == nil {
						s.usageTotal = rec.Usage
					}
				case "regenerate":
					var rec regenerateRecord
					if json.Unmarshal([]byte(line), &rec) == nil && rec.Kept >= 0 && rec.Kept <= len(s.messages) {
						s.messages = s.messages[:rec.Kept]
					}
				case "compaction":
					var rec compactionRecord
					// Kept 负值防护与 regenerate 对齐（issue #9）：负值属
					// 损坏记录，跳过而非 panic。
					if json.Unmarshal([]byte(line), &rec) == nil && rec.Summary != "" && rec.Kept >= 0 {
						kept := rec.Kept
						if kept > len(s.messages) {
							kept = len(s.messages)
						}
						tail := append([]ai.Message(nil), s.messages[len(s.messages)-kept:]...)
						s.messages = append([]ai.Message{compact.SummaryMessage(rec.Summary)}, tail...)
					}
				}
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				return nil, fmt.Errorf("reading %s: %w", path, rerr)
			}
			break
		}
	}
	if s.info.ID == "" {
		return nil, fmt.Errorf("%s has no meta record", path)
	}
	// The title may postdate the last meta rewrite (crash between the two
	// writes): derive it from the transcript like Append would have.
	if s.info.Title == "" {
		for _, m := range s.messages {
			if m.Role == ai.RoleUser {
				s.info.Title = titleFrom(m.Text())
				break
			}
		}
	}
	if stat, err := f.Stat(); err == nil {
		s.info.UpdatedAt = stat.ModTime()
	}
	// 与 Chat 入口一致地重放微压缩，恢复后的上下文与运行时一致。
	if pruned, changed := compact.Microcompact(s.messages, compact.DefaultKeepTail); changed {
		s.messages = pruned
	}
	return s, nil
}

// Manager owns the session directory.
type Manager struct {
	mu       sync.Mutex
	dir      string
	sessions map[string]*Session

	// wiring, installed by the service layer before first use
	wiring Wiring
}

// Wiring carries the loop dependencies the service layer installs.
type Wiring struct {
	Provider func() ai.Provider
	Model    func() string
	// Tools builds a session's tool set; sessionID lets stateful tools
	// (todo) keep their state across turns.
	Tools  func(workspace, sessionID string) []Tool
	System func(workspace string) string
	// Ask resolves permission asks with the user; nil denials stay safe.
	Ask func(AskRequest) AskResponse
	// Thinking returns the session's reasoning effort ("off" default).
	Thinking func() ai.ThinkingLevel
	// Hooks observe and shape every tool call (settings 配置的外部命令
	// 钩子经 HooksFromCommands 装配)；nil 无钩子。
	Hooks Hooks
	// Question delivers the ask tool's questions to the user.
	Question func(ctx context.Context, q Question) (string, error)
	// ProjectRules loads the workspace's project rules (its .zgorules.json);
	// nil or a failed load seeds an empty set, never blocks the session.
	ProjectRules func(workspace string) permission.Ruleset
}

// NewManager loads the sessions of dir, creating it when missing.
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating session directory: %w", err)
	}
	m := &Manager{dir: dir, sessions: map[string]*Session{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		s, err := loadSessionSafe(filepath.Join(dir, e.Name()))
		if err != nil {
			// 加载失败要可见（issue #10）：静默跳过曾让用户以为会话丢了。
			log.Printf("session %s: load failed: %v", e.Name(), err)
			continue
		}
		m.sessions[s.info.ID] = s
	}
	return m, nil
}

// Wire installs the loop dependencies on every session, existing and new.
// It must be called once, before the first Chat.
func (m *Manager) Wire(w Wiring) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wiring = w
	for _, s := range m.sessions {
		m.wire(s)
	}
}

func (m *Manager) wire(s *Session) {
	s.provider = func() ai.Provider {
		if m.wiring.Provider == nil {
			return failProvider{msg: "no provider configured"}
		}
		return m.wiring.Provider()
	}
	s.model = func() string {
		if m.wiring.Model == nil {
			return ""
		}
		return m.wiring.Model()
	}
	s.thinking = m.wiring.Thinking
	s.hooks = m.wiring.Hooks
	s.buildTools = m.wiring.Tools
	if s.buildTools == nil {
		s.buildTools = func(string, string) []Tool { return nil }
	}
	s.system = m.wiring.System
	if s.system == nil {
		s.system = func(string) string { return "" }
	}
	s.ask = m.wiring.Ask
	if s.permissions == nil {
		var rules permission.Ruleset
		if m.wiring.ProjectRules != nil {
			rules = m.wiring.ProjectRules(s.info.Workspace)
		}
		s.permissions = permission.New(rules)
	}
}

// New creates a session for a workspace.
func (m *Manager) New(workspace string) (*Session, error) {
	id := time.Now().UTC().Format("20060102T150405.000000000")
	s := &Session{
		info: SessionInfo{ID: id, Workspace: workspace, UpdatedAt: time.Now()},
		path: filepath.Join(m.dir, id+".jsonl"),
	}
	s.mu.Lock()
	err := s.writeMetaLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.info.ID] = s
	m.wire(s)
	return s, nil
}

// SessionPermissions returns a session's permission service, or nil.
func (m *Manager) SessionPermissions(id string) *permission.Service {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil
	}
	return s.permissions
}

// Get returns a session by id.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List returns sessions newest-first.
func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		infos = append(infos, s.Info())
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})
	return infos
}

// Delete removes a session from the index and disk.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	s.Abort()
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// failProvider is the placeholder used before the manager is wired.
type failProvider struct{ msg string }

func (p failProvider) Stream(context.Context, ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 1)
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopError, ErrorMessage: p.msg}
	close(out)
	return out
}

// ForkAtUserTurn branches a session (ZCode's session fork): the new
// session copies everything before the nth (1-based) user message — the
// fork point stays intact in the source.
func (m *Manager) ForkAtUserTurn(src *Session, userTurn int) (*Session, error) {
	if src == nil || userTurn < 1 {
		return nil, fmt.Errorf("fork needs a user turn >= 1")
	}
	messages := src.Messages()
	cut := -1
	seen := 0
	for i, msg := range messages {
		if msg.Role == ai.RoleUser {
			seen++
			if seen == userTurn {
				cut = i
				break
			}
		}
	}
	if cut < 0 {
		return nil, fmt.Errorf("session has no user turn %d", userTurn)
	}
	fork, err := m.New(src.Info().Workspace)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	wiring := m.wiring
	m.mu.Unlock()
	fork.provider, fork.model = src.provider, src.model
	fork.buildTools, fork.system, fork.ask = src.buildTools, src.system, src.ask
	fork.thinking = src.thinking
	fork.permissions = permission.New(func() permission.Ruleset {
		if wiring.ProjectRules == nil {
			return permission.Ruleset{}
		}
		return wiring.ProjectRules(src.Info().Workspace)
	}())
	for _, msg := range messages[:cut] {
		if err := fork.Append(msg); err != nil {
			return fork, err
		}
	}
	title := src.Info().Title
	if title != "" {
		fork.mu.Lock()
		fork.info.Title = title + "（分叉）"
		fork.metaTitle = fork.info.Title
		fork.mu.Unlock()
	}
	return fork, nil
}
