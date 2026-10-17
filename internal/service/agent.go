// Package service is the boundary between the UI and the agent core: it
// owns settings, the session manager, and the workspace dialog. The UI
// (native, package ui) never touches agent, ai or tools directly
// (specs/desktop-shell.md).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/compact"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	aiAuth "github.com/skeleton1231/zcode-my-go/internal/ai/auth"
	"github.com/skeleton1231/zcode-my-go/internal/checkpoint"
	exporthtml "github.com/skeleton1231/zcode-my-go/internal/export"
	"github.com/skeleton1231/zcode-my-go/internal/mcp"
	"github.com/skeleton1231/zcode-my-go/internal/prompt"
	"github.com/skeleton1231/zcode-my-go/internal/schedule"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// DefaultBaseURL is the Anthropic Messages API; GLM and other compatible
// endpoints override it in settings.
const DefaultBaseURL = "https://api.anthropic.com"

// settings is the persisted user configuration.
type settings struct {
	BaseURL    string                `json:"baseUrl"`
	APIKey     string                `json:"apiKey"`
	Model      string                `json:"model"`
	API        string                `json:"api"`
	Thinking   string                `json:"thinking"` // off|low|medium|high
	MCPServers map[string]mcp.Config `json:"mcpServers"`
	Hooks      agent.CommandHooks    `json:"hooks"`
	OAuth      aiAuth.Token          `json:"oauth"` // subscription login (/login)
}

// SettingsView is what the UI sees: the key never crosses to it in full.
type SettingsView struct {
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	API       string `json:"api"`
	Thinking  string `json:"thinking"`
	HasAPIKey bool   `json:"hasApiKey"`
	KeyHint   string `json:"keyHint"` // e.g. "sk-…abcd", for display only
}

// AgentService is the application service the UI calls.
type AgentService struct {
	mu               sync.Mutex
	settings         settings
	path             string
	mgr              *agent.Manager
	ask              func(agent.AskRequest) agent.AskResponse
	question         func(ctx context.Context, q agent.Question) (string, error)
	states           map[string]*tools.States // session-scoped tool state
	mcp              []*mcp.Server            // launched MCP servers
	memories         map[string]*tools.Memory // workspace-scoped long-term memory
	memRoot          string
	wsCache          map[string]tools.Workspace // sessionID → (possibly remote) workspace
	wsClose          map[string]func()          // sessionID → dial cleanup
	askMu            sync.Mutex                 // asker 单槽串行化（issue #79）
	providerOverride func() ai.Provider         // e2e 注入（实例级，非全局）
	sched            *schedule.Scheduler        // 定时自动化（specs/automation.md）
}

// New builds the service: loads settings, loads sessions, wires the loop.
func New(dataDir string) (*AgentService, error) {
	s := &AgentService{
		path:     filepath.Join(dataDir, "settings.json"),
		states:   map[string]*tools.States{},
		memories: map[string]*tools.Memory{},
		memRoot:  filepath.Join(dataDir, "memory"),
		wsCache:  map[string]tools.Workspace{},
		wsClose:  map[string]func(){},
	}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	mgr, err := agent.NewManager(filepath.Join(dataDir, "sessions"))
	if err != nil {
		return nil, err
	}
	s.mgr = mgr
	s.startMCP()
	s.rewire()
	return s, nil
}

// syncMCP reconciles running servers with the configured set (hot
// reload): stopped-and-restarted for changed signatures, closed for
// removed ones, started for additions; failures degrade, never block.
func (s *AgentService) syncMCP() {
	s.mu.Lock()
	current := map[string]*mcp.Server{}
	for _, srv := range s.mcp {
		current[srv.Name] = srv
	}
	desired := s.settings.MCPServers
	keep := map[string]bool{}
	type pending struct {
		name string
		cfg  mcp.Config
		sig  string
	}
	var toStart []pending
	for name, cfg := range desired {
		sig := mcpSignature(cfg)
		if srv, ok := current[name]; ok && srv.Signature == sig {
			keep[name] = true
			continue
		}
		if srv, ok := current[name]; ok {
			srv.Close() // 配置变了：先停旧的
		}
		if cfg.Command != "" {
			toStart = append(toStart, pending{name: name, cfg: cfg, sig: sig})
			keep[name] = true
		}
	}
	s.mcp = nil // 重建列表：保留未变的 + 待启动的稍后补
	for name, srv := range current {
		if keep[name] {
			s.mcp = append(s.mcp, srv)
		}
	}
	s.mu.Unlock()

	// 启动在锁外（握手最长 20s），成功后再入表。
	for _, p := range toStart {
		server, err := mcp.Start(context.Background(), p.name, p.cfg)
		if err != nil {
			log.Printf("mcp reload: %v", err)
			continue
		}
		server.Signature = p.sig
		s.mu.Lock()
		s.mcp = append(s.mcp, server)
		s.mu.Unlock()
	}
}

// mcpSignature serializes a config's identity: command, args, env.
func mcpSignature(cfg mcp.Config) string {
	keys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(cfg.Command + "|")
	for _, a := range cfg.Args {
		b.WriteString(a + "|")
	}
	for _, k := range keys {
		b.WriteString(k + "=" + cfg.Env[k] + "|")
	}
	return b.String()
}

// startMCP is syncMCP over an empty set (first run); kept as the
// constructor's entry point.
func (s *AgentService) startMCP() { s.syncMCP() }

// Close shuts down MCP servers and every session's background jobs.
func (s *AgentService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, server := range s.mcp {
		server.Close()
	}
	s.mcp = nil
	for _, state := range s.states {
		state.Jobs.KillAll()
	}
	for _, close := range s.wsClose {
		close()
	}
	s.wsClose = map[string]func(){}
}

// rewire installs the current settings into the manager. Callers hold s.mu.
func (s *AgentService) rewire() {
	cfg := s.resolve()
	s.mgr.Wire(agent.Wiring{
		Provider: func() ai.Provider {
			if s.providerOverride != nil {
				return s.providerOverride()
			}
			return providerFor(cfg, s.persistOAuth)
		},
		Model:    func() string { return cfg.Model },
		Thinking: func() ai.ThinkingLevel { return thinkingLevelOf(s.resolve().Thinking) },
		// 外部命令钩子来自设置文件（用户自有配置，非工作区），装配后
		// 随每个会话生效；配置为空时无钩子。
		Hooks: agent.HooksFromCommands(s.settings.Hooks),
		Tools: func(workspace, sessionID string) []agent.Tool {
			s.mu.Lock()
			state, ok := s.states[sessionID]
			if !ok {
				state = tools.NewStates()
				s.states[sessionID] = state
			}
			s.mu.Unlock()
			provider := func() ai.Provider { return providerFor(cfg, s.persistOAuth) }
			model := cfg.Model
			question := s.question
			task := tools.NewTask(func(ctx context.Context, prompt string) (agent.SubtaskResult, error) {
				return agent.RunSubtask(ctx, agent.SubtaskConfig{
					Provider: provider(),
					Model:    model,
					System:   agent.SubtaskSystem(),
					Tools:    tools.ReadOnly(workspace),
				}, prompt)
			})
			ws := s.workspaceFor(sessionID, workspace)
			all := append(tools.AllW(ws, state, question), task,
				tools.NewMemoryTool(s.memoryFor(workspace)))
			wfProvider := func() ai.Provider { return providerFor(cfg, s.persistOAuth) }
			wfModel := cfg.Model
			wfPerms := permissionFor(s, sessionID)
			wfAsk := s.ask
			dispProvider := func() ai.Provider { return providerFor(cfg, s.persistOAuth) }
			dispModel := cfg.Model
			all = append(all, tools.NewDispatch(func(ctx context.Context, prompts []string) []agent.SubtaskResult {
				return agent.RunSubtasks(ctx, agent.SubtaskConfig{
					Provider: dispProvider(),
					Model:    dispModel,
					System:   agent.SubtaskSystem(),
					Tools:    tools.ReadOnly(workspace),
				}, prompts)
			}))
			all = append(all, tools.NewWorkflow(func(ctx context.Context, steps []agent.WorkflowStep) ([]agent.StepResult, error) {
				return agent.RunWorkflow(ctx, agent.WorkflowConfig{
					Provider:    wfProvider(),
					Model:       wfModel,
					System:      prompt.System(workspace),
					ReadOnly:    tools.ReadOnly(workspace),
					Editable:    tools.AllW(ws, state, question),
					Permissions: wfPerms,
					Ask:         wfAsk,
				}, steps)
			}))
			for _, server := range s.mcpServers() {
				all = append(all, mcp.AgentTools(server)...)
			}
			return all
		},
		System: func(workspace string) string {
			return prompt.System(workspace) + s.memoryFor(workspace).Section()
		},
		Ask:          s.ask,
		ProjectRules: loadProjectRules,
		Question:     s.question,
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
	if v := os.Getenv("ZGO_API"); v != "" {
		cfg.API = v
	}
	if v := os.Getenv("ZGO_THINKING"); v != "" {
		cfg.Thinking = v
	}
	if cfg.API == "" {
		cfg.API = "anthropic"
	}
	return cfg
}

// thinkingLevelOf validates a configured level, defaulting to off.
func thinkingLevelOf(v string) ai.ThinkingLevel {
	switch ai.ThinkingLevel(v) {
	case ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh:
		return ai.ThinkingLevel(v)
	default:
		return ai.ThinkingOff
	}
}

// providerFor builds the provider matching the configured API dialect.
// An Anthropic subscription token (from /login) authenticates as Bearer
// when no API key is set; an expired token refreshes synchronously
// (best effort — a failed refresh falls back to the stale token).
// persist（可空）在刷新成功后回写凭据：providerFor 的 cfg 是 rewire 时
// 的快照，不回写会让每次构建 provider 都重新刷新（网络浪费），且
// 服务端轮换 refresh token 后存储的旧凭据失效 → 长会话 401。
func providerFor(cfg settings, persist func(aiAuth.Token)) ai.Provider {
	switch cfg.API {
	case "openai":
		return ai.NewOpenAI(cfg.BaseURL, cfg.APIKey)
	case "gemini":
		return ai.NewGemini(cfg.BaseURL, cfg.APIKey)
	default:
		client := ai.NewAnthropic(cfg.BaseURL, cfg.APIKey)
		if cfg.APIKey == "" && cfg.OAuth.AccessToken != "" {
			if cfg.OAuth.Expired() && cfg.OAuth.RefreshToken != "" {
				if fresh, err := aiAuth.Refresh(context.Background(),
					authTokenURL, cfg.OAuth.RefreshToken); err == nil {
					cfg.OAuth = fresh
					client.OAuthToken = fresh.AccessToken
					if persist != nil {
						persist(fresh)
					}
					return client
				}
			}
			client.OAuthToken = cfg.OAuth.AccessToken
		}
		return client
	}
}

// persistOAuth 回写订阅凭据：更新内存设置并落盘（0600，与 API key 同
// 一安全边界）；失败只记日志，不影响本次运行。
func (s *AgentService) persistOAuth(tok aiAuth.Token) {
	s.mu.Lock()
	cfg := s.settings
	cfg.OAuth = tok
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		err = os.WriteFile(s.path, data, 0o600)
	}
	if err == nil {
		s.settings = cfg
	}
	s.mu.Unlock()
	if err != nil {
		log.Printf("persisting oauth token: %v", err)
	}
}

// authTokenURL is the token endpoint (pi's constant).
const authTokenURL = aiAuth.TokenURL

// LoginStatus reports whether a subscription token is present.
func (s *AgentService) LoginStatus() (ok bool, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := s.settings.OAuth
	return tok.AccessToken != "", tok.ExpiresAt
}

// LoginStart runs the OAuth flow in the background: onURL receives the
// browser link as soon as the callback server is up; the token lands in
// settings when the browser round-trip completes. done reports success.
func (s *AgentService) LoginStart(onURL func(string)) <-chan error {
	done := make(chan error, 1)
	go func() {
		tok, err := aiAuth.Login(context.Background(), func(link string) {
			if onURL != nil {
				onURL(link)
			}
		}, authTokenURL)
		if err != nil {
			done <- err
			return
		}
		s.mu.Lock()
		cfg := s.settings
		cfg.OAuth = tok
		data, merr := json.MarshalIndent(cfg, "", "  ")
		if merr == nil {
			merr = os.WriteFile(s.path, data, 0o600)
		}
		if merr == nil {
			s.settings = cfg
		}
		s.mu.Unlock()
		s.rewire()
		done <- merr
	}()
	return done
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
	v := SettingsView{BaseURL: cfg.BaseURL, Model: cfg.Model, API: cfg.API,
		Thinking: cfg.Thinking, HasAPIKey: cfg.APIKey != ""}
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
func (s *AgentService) SaveSettings(api, baseURL, apiKey, model, thinking string) (SettingsView, error) {
	// 写盘与 rewire 在锁内；syncMCP 自带加锁且启动在锁外（握手最长
	// 20s），必须在 SaveSettings 释放锁之后调用——否则自死锁。
	s.mu.Lock()
	cfg := s.settings
	if api == "openai" || api == "anthropic" || api == "gemini" {
		cfg.API = api
	}
	switch thinking {
	case "low", "medium", "high", "off":
		cfg.Thinking = thinking
	}
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	if apiKey != "" {
		cfg.APIKey = apiKey
	}
	cfg.Model = model
	view, err := s.saveSettingsLocked(cfg)
	s.mu.Unlock()
	if err == nil {
		s.syncMCP() // 配置变更热重载
	}
	return view, err
}

// saveSettingsLocked persists cfg and installs it; callers hold s.mu.
func (s *AgentService) saveSettingsLocked(cfg settings) (SettingsView, error) {
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
	// ask 全局串行化（issue #79）：确认通道（桌面 modal、终端 stdin）
	// 都是单槽，跨会话/跨回合并发 ask 会覆盖槽内状态、孤儿化先到者的
	// 应答 channel。per-Loop 的 askMu 只覆盖单次运行，这里在 asker
	// 装配处把串行化上移到与单槽同层级。
	s.ask = func(req agent.AskRequest) agent.AskResponse {
		s.askMu.Lock()
		defer s.askMu.Unlock()
		return ask(req)
	}
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
	// 浏览器目标没有本地目录选择器（工作区本身就在 wasm 的内存 FS 上）：
	// 固定落在 /zgo-workspace，会话期间有效。
	if runtime.GOOS == "js" {
		const dir = "/zgo-workspace"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return dir, nil
	}
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

// PickImages opens a multi-select image picker.
func (s *AgentService) PickImages(parent *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:   parent,
		Title:    "添加图片",
		Filters:  []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}},
		Multiple: true,
	})
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

// DeleteSession removes a session (its background jobs stop first).
func (s *AgentService) DeleteSession(id string) error {
	s.mu.Lock()
	if state, ok := s.states[id]; ok {
		state.Jobs.KillAll()
		delete(s.states, id)
	}
	if close, ok := s.wsClose[id]; ok {
		close()
		delete(s.wsClose, id)
	}
	delete(s.wsCache, id)
	s.mu.Unlock()
	return s.mgr.Delete(id)
}

// SessionWorkspace returns the session's workspace path ("" when the
// session is unknown).
func (s *AgentService) SessionWorkspace(sessionID string) string {
	if sess, ok := s.mgr.Get(sessionID); ok {
		return sess.Info().Workspace
	}
	return ""
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
	return s.ChatMedia(sessionID, text, nil, emit)
}

// ChatMedia is Chat carrying image attachments（composer 的 📎）。
func (s *AgentService) ChatMedia(sessionID, text string, media []ai.Block, emit func(agent.Event)) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	// 回合开始前快照工作区（本地 git 仓库时）：seq = 新用户消息的序号
	//（现有用户消息数），回滚按序号定位。
	s.captureCheckpoint(sess, userTurnCount(sess.Messages()))
	text, media = s.expandMentions(sessionID, text, media)
	return sess.ChatMedia(text, media, emit)
}

// captureCheckpoint snapshots the workspace before a turn. Non-repo and
// SSH-remote workspaces degrade silently to conversation-only rewind.
func (s *AgentService) captureCheckpoint(sess *agent.Session, seq int) {
	info := sess.Info()
	if strings.HasPrefix(info.Workspace, "ssh://") {
		return
	}
	if err := checkpoint.Capture(info.Workspace, info.ID, seq); err != nil &&
		!errors.Is(err, checkpoint.ErrNotARepo) {
		log.Printf("checkpoint capture: %v", err)
	}
}

// restoreCheckpoint rolls the workspace back to a turn's start. 非仓库与
// 无检查点按仅回退转录降级（返回 nil）；其余失败原样返回——调用方必须
// 中止后续的转录回退（issue #110：restore 失败仍回退转录会造出
// "对话说改没了、文件还在"的分裂态）。
func (s *AgentService) restoreCheckpoint(sess *agent.Session, seq int) error {
	info := sess.Info()
	if strings.HasPrefix(info.Workspace, "ssh://") {
		return nil
	}
	if err := checkpoint.Restore(info.Workspace, info.ID, seq); err != nil &&
		!errors.Is(err, checkpoint.ErrNotARepo) &&
		!errors.Is(err, checkpoint.ErrNoCheckpoint) {
		return fmt.Errorf("restoring workspace checkpoint: %w", err)
	}
	return nil
}

// userTurnCount is the number of user turns in a transcript — the ordinal
// of the next user message, and (minus one) of the last.
func userTurnCount(msgs []ai.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == ai.RoleUser {
			n++
		}
	}
	return n
}

// RewindLatest undoes the last whole turn: the workspace restores to the
// checkpoint taken before it and the transcript drops the exchange (the
// 会话 menu's 回滚最近一回合).
func (s *AgentService) RewindLatest(sessionID string) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	// 运行守卫必须先于文件恢复（issue #110）：活回合的工作树一旦被
	// read-tree/clean 抽走，运行中的工具按旧假设继续读写，而转录回退
	// 又在守卫处失败——文件动了、对话没回，最坏的不一致态。cron 对同
	// 一 workspace 的后台会话可绕过 UI 守卫直达此处。
	if sess.Running() {
		return fmt.Errorf("a run is already in progress")
	}
	msgs := sess.Messages()
	if userTurnCount(msgs) == 0 {
		return fmt.Errorf("no turn to rewind")
	}
	if err := s.restoreCheckpoint(sess, userTurnCount(msgs)-1); err != nil {
		return err
	}
	return sess.Rewind(agent.LastUserIndex(msgs))
}

// SessionRunning reports whether the session has a run in flight.
func (s *AgentService) SessionRunning(id string) bool {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return false
	}
	return sess.Running()
}

// CompactSession summarizes a session's old history (the 压缩会话 menu).
func (s *AgentService) CompactSession(id string) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	return sess.Compact(context.Background())
}

// PlanMode switches a session's collaboration mode (plan = read-only).
func (s *AgentService) PlanMode(id string, on bool) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	sess.SetPlanMode(on)
	return nil
}

// memoryFor returns (creating) the workspace's memory store.
func (s *AgentService) memoryFor(workspace string) *tools.Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem, ok := s.memories[workspace]
	if !ok {
		mem = tools.NewMemory(tools.MemoryPath(s.memRoot, workspace))
		s.memories[workspace] = mem
	}
	return mem
}

// permissionFor returns the session's permission service for workflow
// inheritance. Callers hold s.mu.
func permissionFor(s *AgentService, sessionID string) *permission.Service {
	return s.mgr.SessionPermissions(sessionID)
}

// rulesFile is the per-workspace project rules file, committed with the
// repo (ZCode's project permission rules).
const rulesFile = ".zgorules.json"

// rulesCapBytes bounds the rules file a workspace may impose.
const rulesCapBytes = 16 << 10

// loadProjectRules reads the workspace's .zgorules.json; missing files and
// parse failures degrade to an empty set (logged), never a blocked session.
func loadProjectRules(workspace string) permission.Ruleset {
	if workspace == "" {
		return permission.Ruleset{}
	}
	data, err := os.ReadFile(filepath.Join(workspace, rulesFile))
	if err != nil {
		return permission.Ruleset{}
	}
	if len(data) > rulesCapBytes {
		log.Printf("rules: %s in %s exceeds %d bytes, ignored", rulesFile, workspace, rulesCapBytes)
		return permission.Ruleset{}
	}
	rules, err := permission.ParseRuleset(data)
	if err != nil {
		log.Printf("rules: %s in %s: %v (ignored)", rulesFile, workspace, err)
		return permission.Ruleset{}
	}
	return rules
}

// workspaceFor returns (dialing once) the session's workspace: local, or
// an SSH workspace when the target is ssh://.
func (s *AgentService) workspaceFor(sessionID, workspace string) tools.Workspace {
	s.mu.Lock()
	if ws, ok := s.wsCache[sessionID]; ok {
		s.mu.Unlock()
		return ws
	}
	s.mu.Unlock()
	ws, cleanup, err := s.openWorkspace(workspace)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// 拨号失败退化为本地路径工作区：会话仍可用，远程工具报错自纠。
		s.wsCache[sessionID] = tools.Workspace{Root: workspace}
		return tools.Workspace{Root: workspace}
	}
	if cleanup != nil {
		s.wsClose[sessionID] = cleanup
	}
	s.wsCache[sessionID] = ws
	return ws
}

// mcpServers snapshots the launched servers. Callers hold s.mu.
func (s *AgentService) mcpServers() []*mcp.Server {
	return s.mcp
}

// EditAndResend replaces the session's last user message and reruns.
func (s *AgentService) EditAndResend(sessionID, text string, emit func(agent.Event)) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	// 编辑重发即回退重跑：工作区一并恢复到该回合前（运行守卫先于文件
	// 恢复，同 RewindLatest；EditAndResend 内部守卫此时必然通过）。
	if sess.Running() {
		return fmt.Errorf("a run is already in progress")
	}
	if err := s.restoreCheckpoint(sess, userTurnCount(sess.Messages())-1); err != nil {
		return err
	}
	return sess.EditAndResend(text, emit)
}

// ForkSession branches a session before its nth user message.
func (s *AgentService) ForkSession(sessionID string, userTurn int) (agent.SessionInfo, error) {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return agent.SessionInfo{}, fmt.Errorf("no session %s", sessionID)
	}
	fork, err := s.mgr.ForkAtUserTurn(sess, userTurn)
	if err != nil {
		return agent.SessionInfo{}, err
	}
	return fork.Info(), nil
}

// ContextBudget reports a session's estimated context usage against its
// model's window and the auto-compact threshold (ZCode's context
// indicator): the numbers behind the status bar's meter.
func (s *AgentService) ContextBudget(id string) (ContextBudget, error) {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return ContextBudget{}, fmt.Errorf("no session %s", id)
	}
	window := ai.LookupModel(s.resolve().Model).ContextWindow
	policy := compact.DefaultPolicy()
	policy.ContextWindow = window
	return ContextBudget{
		Used:      compact.EstimateTokens(sess.Messages()),
		Window:    window,
		Threshold: policy.Threshold(),
	}, nil
}

// ContextBudget is the context meter's data.
type ContextBudget struct {
	Used      int `json:"used"`
	Window    int `json:"window"`
	Threshold int `json:"threshold"`
}

// RenameSession renames a session.
func (s *AgentService) RenameSession(id, title string) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	if err := sess.Rename(title); err != nil {
		return fmt.Errorf("renaming %s: %w", id, err)
	}
	return nil
}

// SessionUsage returns a session's cumulative token usage.
func (s *AgentService) SessionUsage(id string) (ai.Usage, error) {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return ai.Usage{}, fmt.Errorf("no session %s", id)
	}
	return sess.UsageTotal(), nil
}

// PlanModeState reports a session's plan mode.
func (s *AgentService) PlanModeState(id string) bool {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return false
	}
	return sess.PlanMode()
}

// Retry regenerates a session's last answer.
func (s *AgentService) Retry(sessionID string, emit func(agent.Event)) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	// 重生成前把工作区恢复到该回合开始前（运行守卫先于文件恢复，同
	// RewindLatest；Retry 内部守卫此时必然通过）。
	if sess.Running() {
		return fmt.Errorf("a run is already in progress")
	}
	if err := s.restoreCheckpoint(sess, userTurnCount(sess.Messages())-1); err != nil {
		return err
	}
	return sess.Retry(emit)
}

// ModelSuggestions lists catalog models for the configured dialect.
func (s *AgentService) ModelSuggestions() []string {
	cfg := s.resolve()
	var out []string
	for _, m := range ai.CatalogModels(cfg.API) {
		out = append(out, m.ID)
	}
	return out
}

// ExportSessionHTML writes a session's transcript as a standalone HTML
// file to dest.
func (s *AgentService) ExportSessionHTML(id, dest string) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	info := sess.Info()
	doc := exporthtml.HTML(sess.Messages(), info.Title, s.resolve().Model, info.Workspace)
	return os.WriteFile(dest, []byte(doc), 0o644)
}

// TodoPlan returns the session's live task plan (the todo tool's state).
func (s *AgentService) TodoPlan(sessionID string) []tools.TodoTask {
	s.mu.Lock()
	state, ok := s.states[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return state.Todo.Snapshot()
}

// ExportSessionMarkdown writes the transcript as Markdown.
func (s *AgentService) ExportSessionMarkdown(id, dest string) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	info := sess.Info()
	doc := exporthtml.Markdown(sess.Messages(), info.Title, s.resolve().Model, info.Workspace)
	return os.WriteFile(dest, []byte(doc), 0o644)
}

// Steer queues a message into a running session (pi's steering).
func (s *AgentService) Steer(sessionID, text string) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	return sess.Steer(text)
}

// UseProviderForTesting swaps the provider factory (e2e only). 覆盖是
// 实例字段而非包级全局（review #87：全局会让未来任何并行测试静默互踩）。
func (s *AgentService) UseProviderForTesting(fn func() ai.Provider) {
	s.mu.Lock()
	s.providerOverride = fn
	s.mu.Unlock()
	s.rewire()
}

// Abort cancels a session's running loop.
func (s *AgentService) Abort(sessionID string) {
	if sess, ok := s.mgr.Get(sessionID); ok {
		sess.Abort()
	}
}
