// Package service is the boundary between the UI and the agent core: it
// owns settings, the session manager, and the workspace dialog. The UI
// (native, package ui) never touches agent, ai or tools directly
// (specs/desktop-shell.md).
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/prompt"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// DefaultBaseURL is the Anthropic Messages API; GLM and other compatible
// endpoints override it in settings.
const DefaultBaseURL = "https://api.anthropic.com"

// settings is the persisted user configuration.
type settings struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
}

// SettingsView is what the UI sees: the key never crosses to it in full.
type SettingsView struct {
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	HasAPIKey bool   `json:"hasApiKey"`
	KeyHint   string `json:"keyHint"` // e.g. "sk-…abcd", for display only
}

// AgentService is the application service the UI calls.
type AgentService struct {
	mu       sync.Mutex
	settings settings
	path     string
	mgr      *agent.Manager
	ask      func(agent.AskRequest) agent.AskResponse
	question func(ctx context.Context, q agent.Question) (string, error)
	states   map[string]*tools.States // session-scoped tool state
}

// New builds the service: loads settings, loads sessions, wires the loop.
func New(dataDir string) (*AgentService, error) {
	s := &AgentService{
		path:   filepath.Join(dataDir, "settings.json"),
		states: map[string]*tools.States{},
	}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	mgr, err := agent.NewManager(filepath.Join(dataDir, "sessions"))
	if err != nil {
		return nil, err
	}
	s.mgr = mgr
	s.rewire()
	return s, nil
}

// rewire installs the current settings into the manager. Callers hold s.mu.
func (s *AgentService) rewire() {
	cfg := s.resolve()
	s.mgr.Wire(agent.Wiring{
		Provider: func() ai.Provider { return ai.NewAnthropic(cfg.BaseURL, cfg.APIKey) },
		Model:    func() string { return cfg.Model },
		Tools: func(workspace, sessionID string) []agent.Tool {
			s.mu.Lock()
			state, ok := s.states[sessionID]
			if !ok {
				state = tools.NewStates()
				s.states[sessionID] = state
			}
			s.mu.Unlock()
			return tools.All(workspace, state, s.question)
		},
		System:   prompt.System,
		Ask:      s.ask,
		Question: s.question,
	})
}

// resolve applies env overrides to the stored settings.
func (s *AgentService) resolve() settings {
	cfg := s.settings
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("ZGO_MODEL"); v != "" {
		cfg.Model = v
	}
	return cfg
}

// loadSettings reads the settings file; a missing file is first run.
func (s *AgentService) loadSettings() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading settings: %w", err)
	}
	return json.Unmarshal(data, &s.settings)
}

// view projects the resolved settings for the UI.
func (s *AgentService) view() SettingsView {
	cfg := s.resolve()
	v := SettingsView{BaseURL: cfg.BaseURL, Model: cfg.Model, HasAPIKey: cfg.APIKey != ""}
	if n := len(cfg.APIKey); n > 8 {
		v.KeyHint = cfg.APIKey[:3] + "…" + cfg.APIKey[n-4:]
	} else if n > 0 {
		v.KeyHint = "…"
	}
	return v
}

// GetSettings returns the resolved settings, without the API key.
func (s *AgentService) GetSettings() SettingsView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view()
}

// SaveSettings stores the configuration. An empty apiKey keeps the stored
// key, so the UI never needs to echo it back.
func (s *AgentService) SaveSettings(baseURL, apiKey, model string) (SettingsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.settings
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	if apiKey != "" {
		cfg.APIKey = apiKey
	}
	cfg.Model = model
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return s.view(), err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return s.view(), err
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return s.view(), fmt.Errorf("writing settings: %w", err)
	}
	s.settings = cfg
	s.rewire()
	return s.view(), nil
}

// SetAsker installs the callback that resolves permission asks with the
// user (a modal in the desktop UI, a prompt on the CLI).
func (s *AgentService) SetAsker(ask func(agent.AskRequest) agent.AskResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ask = ask
	s.rewire()
}

// SetQuestion installs the callback that delivers the ask tool's
// questions to the user.
func (s *AgentService) SetQuestion(fn func(ctx context.Context, q agent.Question) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.question = fn
	s.rewire()
}

// PickWorkspace asks the user for a workspace directory. parent may be nil.
func (s *AgentService) PickWorkspace(parent *mygo.Window) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    parent,
		Directory: true,
		Title:     "选择工作区",
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// NewSession creates a session for a workspace and returns its projection.
func (s *AgentService) NewSession(workspace string) (agent.SessionInfo, error) {
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return agent.SessionInfo{}, err
		}
		workspace = cwd
	}
	sess, err := s.mgr.New(workspace)
	if err != nil {
		return agent.SessionInfo{}, err
	}
	return sess.Info(), nil
}

// ListSessions returns the session index, newest first.
func (s *AgentService) ListSessions() []agent.SessionInfo {
	return s.mgr.List()
}

// DeleteSession removes a session.
func (s *AgentService) DeleteSession(id string) error {
	s.mu.Lock()
	delete(s.states, id)
	s.mu.Unlock()
	return s.mgr.Delete(id)
}

// Messages returns a session's transcript for replay.
func (s *AgentService) Messages(id string) ([]ai.Message, error) {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return nil, fmt.Errorf("no session %s", id)
	}
	return sess.Messages(), nil
}

// Chat sends a user message and calls emit for every event of the run, on
// the caller's goroutine. The UI wraps emit in Window.Update and calls Chat
// off the main thread.
func (s *AgentService) Chat(sessionID, text string, emit func(agent.Event)) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	return sess.Chat(text, emit)
}

// CompactSession summarizes a session's old history (the 压缩会话 menu).
func (s *AgentService) CompactSession(id string) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	return sess.Compact(context.Background())
}

// Abort cancels a session's running loop.
func (s *AgentService) Abort(sessionID string) {
	if sess, ok := s.mgr.Get(sessionID); ok {
		sess.Abort()
	}
}
