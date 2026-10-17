package agent

import (
	"bufio"
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

	// wiring, installed by the manager
	provider   func() ai.Provider
	model      func() string
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

	if err := s.Append(ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}}); err != nil {
		return err
	}
	s.run(ctx, workspace, id, modelInfo, emit)
	return nil
}

// run drives the loop for one turn sequence (Chat and Retry share it).
func (s *Session) run(ctx context.Context, workspace, id string, modelInfo ai.ModelInfo, emit func(Event)) {
	loop := NewLoop(LoopConfig{
		Provider:    s.provider(),
		Model:       s.model(),
		System:      s.system(workspace),
		Tools:       s.buildTools(workspace, id),
		MaxTokens:   modelInfo.MaxOutput,
		MaxTurns:    DefaultMaxTurns,
		History:     s.Messages,
		SessionID:   id,
		Permissions: s.permissions,
		Ask:         s.ask,
	}, func(ev Event) {
		ev.SessionID = id
		emit(ev)
	})
	loop.Run(ctx, s.Append)
}

// Retry drops everything from the last user message onward and regenerates
// the answer (ZCode's regenerate): the transcript truncation is persisted
// as a regenerate record and replayed on load, keeping the file
// append-only.
func (s *Session) Retry(emit func(Event)) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("a run is already in progress")
	}
	last := -1
	for i, m := range s.messages {
		if m.Role == ai.RoleUser {
			last = i
		}
	}
	if last < 0 {
		s.mu.Unlock()
		return errors.New("nothing to regenerate")
	}
	prompt := s.messages[last].Text()
	s.messages = s.messages[:last]
	kept := last
	workspace := s.info.Workspace
	modelInfo := ai.LookupModel(s.model())
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	id := s.info.ID
	rec, err := json.Marshal(regenerateRecord{Type: "regenerate", Kept: kept})
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
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}); err != nil {
		return err
	}
	s.run(ctx, workspace, id, modelInfo, emit)
	return nil
}

// SetPlanMode switches the session's collaboration mode.
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

// regenerateRecord marks a retry: messages beyond Kept were dropped.
type regenerateRecord struct {
	Type string `json:"type"`
	Kept int    `json:"kept"`
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

// loadSession reads a session file. A missing meta record is fatal; damaged
// message lines are skipped, not fatal.
func loadSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := &Session{path: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var kind struct{ Type string }
		if err := json.Unmarshal([]byte(line), &kind); err != nil {
			continue
		}
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
		case "regenerate":
			var rec regenerateRecord
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Kept >= 0 && rec.Kept <= len(s.messages) {
				s.messages = s.messages[:rec.Kept]
			}
		case "compaction":
			var rec compactionRecord
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Summary != "" {
				kept := rec.Kept
				if kept > len(s.messages) {
					kept = len(s.messages)
				}
				tail := append([]ai.Message(nil), s.messages[len(s.messages)-kept:]...)
				s.messages = append([]ai.Message{compact.SummaryMessage(rec.Summary)}, tail...)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
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
	// Question delivers the ask tool's questions to the user.
	Question func(ctx context.Context, q Question) (string, error)
	// ProjectRules seeds every session's permission service.
	ProjectRules permission.Ruleset
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
		s, err := loadSession(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // unreadable session files are skipped, not fatal
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
		s.permissions = permission.New(m.wiring.ProjectRules)
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
