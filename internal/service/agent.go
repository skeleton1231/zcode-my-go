// Package service is the boundary between the UI and the agent core: it
// owns settings, the session manager, and the workspace dialog. The UI
// (native, package ui) never touches agent, ai or tools directly
// (specs/desktop-shell.md).
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/compact"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/agents"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	aiAuth "github.com/skeleton1231/zcode-my-go/internal/ai/auth"
	"github.com/skeleton1231/zcode-my-go/internal/checkpoint"
	exporthtml "github.com/skeleton1231/zcode-my-go/internal/export"
	"github.com/skeleton1231/zcode-my-go/internal/lsp"
	"github.com/skeleton1231/zcode-my-go/internal/mcp"
	"github.com/skeleton1231/zcode-my-go/internal/prompt"
	"github.com/skeleton1231/zcode-my-go/internal/redact"
	"github.com/skeleton1231/zcode-my-go/internal/schedule"
	"github.com/skeleton1231/zcode-my-go/internal/sessionidx"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
	"github.com/skeleton1231/zcode-my-go/internal/tooldefs"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
	"github.com/skeleton1231/zcode-my-go/internal/trust"
	"github.com/skeleton1231/zcode-my-go/internal/usage"
	"github.com/skeleton1231/zcode-my-go/internal/workflow"
)

// DefaultBaseURL is the Anthropic Messages API; GLM and other compatible
// endpoints override it in settings.
const DefaultBaseURL = "https://api.anthropic.com"

// settings is the persisted user configuration.
type settings struct {
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	API      string `json:"api"`
	Thinking string `json:"thinking"` // off|low|medium|high
	// Profiles 是命名 API 剖面（issue #215）：active 剖面物化进上面的扁
	// 平字段——resolve/env 覆盖与 providerFor 路径零感知；切换即改写扁
	// 平字段并落盘。key 与扁平 key 同一条脱敏边界。
	Profiles map[string]profile `json:"profiles"`
	Active   string             `json:"active"`

	MCPServers map[string]mcp.Config `json:"mcpServers"`
	Hooks      agent.CommandHooks    `json:"hooks"`
	OAuth      aiAuth.Token          `json:"oauth"` // subscription login (/login)；与剖面正交
	// BudgetDailyTokens 是每日软预算（0=不限，spec 规则 35）。
	BudgetDailyTokens int `json:"budgetDailyTokens"`
	// HardBudgetStop 是硬预算熔断（spec 规则 85）：今日已计价 token 达
	// budgetDailyTokens 时拒绝新回合（明确报错——软通知之外的止损
	// 闸；默认关）。cron 调度面同样受闸。
	HardBudgetStop bool `json:"hardBudgetStop,omitempty"`
	// FallbackModels 是模型兜底链（spec 规则 56）：主模型可重试失败
	// （429/过载）时按序降级重发。
	FallbackModels []string `json:"fallbackModels"`
	// RedactSecrets 开关工具输出的密钥打码（spec 规则 73；nil = 默认
	// 开——逃生口供需要原样输出的场景，如生成测试夹具）。
	RedactSecrets *bool `json:"redactSecrets,omitempty"`
	// AutoMemory 开关会话记忆自动化（spec 规则 74；nil = 默认开——
	// 服务收尾为有实质回合的会话提炼一次持久事实进工作区记忆）。
	AutoMemory *bool `json:"autoMemory,omitempty"`
}

// profile 是一个命名 API 剖面。
type profile struct {
	API      string `json:"api"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
}

// SettingsView is what the UI sees: the key never crosses to it in full.
type SettingsView struct {
	BaseURL   string   `json:"baseUrl"`
	Model     string   `json:"model"`
	API       string   `json:"api"`
	Thinking  string   `json:"thinking"`
	HasAPIKey bool     `json:"hasApiKey"`
	KeyHint   string   `json:"keyHint"`  // e.g. "sk-…abcd", for display only
	Profile   string   `json:"profile"`  // 当前活动剖面名（展示/状态栏）
	Profiles  []string `json:"profiles"` // 可用剖面（下拉/列表）
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
	mcpSyncMu        sync.Mutex               // syncMCP 串行化（异步启动后可能与显式重载并发）
	mcpClosed        bool                     // Close 后不再入表——在途握手完成即关闭（防进程泄漏）
	memories         map[string]*tools.Memory // workspace-scoped long-term memory
	memRoot          string
	wsCache          map[string]tools.Workspace                     // sessionID → (possibly remote) workspace
	wsClose          map[string]func()                              // sessionID → dial cleanup
	askMu            sync.Mutex                                     // asker 单槽串行化（issue #79）
	refreshMu        sync.Mutex                                     // OAuth 刷新 single-flight（issue #136）
	providerOverride func() ai.Provider                             // e2e 注入（实例级，非全局）
	autoTitleOn      bool                                           // 首回合后自动命名（默认开）
	autoMemoryOn     bool                                           // 收尾自动记忆（默认开，spec 规则 74）
	memFired         map[string]bool                                // 记忆已发会话（每会话每进程一次）
	memMu            sync.Mutex                                     // memFired 的守卫
	memWG            sync.WaitGroup                                 // 记忆 goroutine 的收束（Close 等待）
	day              *usage.Day                                     // 当日 token 记账（spec 规则 35）
	titleWG          sync.WaitGroup                                 // 命名 goroutine 的收束（Close 等待）
	hookWG           sync.WaitGroup                                 // sessionStart 钩子的收束（Close 等待）
	idxWG            sync.WaitGroup                                 // 搜索索引异步落盘的收束（Close 等待）
	sched            *schedule.Scheduler                            // 定时自动化（specs/automation.md）
	wfStore          *workflow.Store                                // 工作流定义与运行记录（spec 规则 48）
	trustStore       *trust.Store                                   // 工作区授信表（spec 规则 54）
	searchIdx        *sessionidx.Index                              // 跨会话搜索索引（spec 规则 71）
	buildTools       func(workspace, sessionID string) []agent.Tool // 工具面装配（/tools 列举同源）
}

// New builds the service: loads settings, loads sessions, wires the loop.
func New(dataDir string) (*AgentService, error) {
	s := &AgentService{
		path:         filepath.Join(dataDir, "settings.json"),
		states:       map[string]*tools.States{},
		memories:     map[string]*tools.Memory{},
		memRoot:      filepath.Join(dataDir, "memory"),
		wsCache:      map[string]tools.Workspace{},
		wsClose:      map[string]func(){},
		autoTitleOn:  true,
		autoMemoryOn: true,
		memFired:     map[string]bool{},
		wfStore:      workflow.NewStore(dataDir),
		trustStore:   trust.NewStore(dataDir),
		searchIdx:    sessionidx.Load(filepath.Join(dataDir, "search-index.json")),
	}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	s.syncRedaction(s.resolve())
	// 会话记忆自动化开关（spec 规则 74）：nil = 默认开。
	if off := s.resolve().AutoMemory; off != nil && !*off {
		s.autoMemoryOn = false
	}
	// edit/write 诊断回灌（spec 规则 67/69）：gopls 在位即接线（daemon
	// 版——编辑后显式失效再拉取，busy/缺位退 CLI 子进程），缺位静默
	// 跳过（回执无尾部，行为与既往一致）。
	if lsp.Available() {
		tools.ProbeDiagnostics = lsp.DaemonProbe
	}
	s.day = usage.Load(dataDir)
	mgr, err := agent.NewManager(filepath.Join(dataDir, "sessions"))
	if err != nil {
		return nil, err
	}
	s.mgr = mgr
	// 启动异步（性能路线第二条）：mcp.Start 的握手最长 20s——同步曾让
	// 一个挂死的服务器把每个 zgo 命令的启动卡满超时；后台起、握手完
	// 成即入表（工具面随 mcpServers 快照自然出现）。显式重载仍同步
	// （设置动作要看到结果）。
	go s.startMCP()
	s.rewire()
	return s, nil
}

// mcpNameMax 限制配置的服务器数（防误配炸启动面）。
const mcpNameMax = 32

// SetMCPServer 配置/更新一个 MCP 服务器并热重载（spec 规则 58）：名
// 非空且无空白/斜杠（文件与键的安全形态）；命令非空。写盘经
// saveSettingsLocked，启动经 syncMCP（失败降级不阻断——诊断面可见）。
func (s *AgentService) SetMCPServer(name string, cfg mcp.Config) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " /\\") {
		return fmt.Errorf("MCP 服务器名须非空且不含空格/斜杠：%q", name)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return fmt.Errorf("MCP 服务器 %s 缺少 command", name)
	}
	s.mu.Lock()
	if s.settings.MCPServers == nil {
		s.settings.MCPServers = map[string]mcp.Config{}
	}
	if _, exists := s.settings.MCPServers[name]; !exists && len(s.settings.MCPServers) >= mcpNameMax {
		s.mu.Unlock()
		return fmt.Errorf("MCP 服务器已达上限 %d", mcpNameMax)
	}
	s.settings.MCPServers[name] = cfg
	_, err := s.saveSettingsLocked(s.settings)
	s.mu.Unlock()
	if err == nil {
		s.syncMCP() // 热重载：新服务器握手失败会降级（诊断面可见）
	}
	return err
}

// MCPServers 返回当前配置的服务器表（copy）——/mcp 列表与测试断言面。
func (s *AgentService) MCPServers() map[string]mcp.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]mcp.Config, len(s.settings.MCPServers))
	for k, v := range s.settings.MCPServers {
		out[k] = v
	}
	return out
}

// RemoveMCPServer 移除一个 MCP 服务器并热重载（不存在的名字报错）。
func (s *AgentService) RemoveMCPServer(name string) error {
	s.mu.Lock()
	if _, ok := s.settings.MCPServers[name]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("MCP 服务器 %s 不存在（/mcp 查看）", name)
	}
	delete(s.settings.MCPServers, name)
	_, err := s.saveSettingsLocked(s.settings)
	s.mu.Unlock()
	if err == nil {
		s.syncMCP()
	}
	return err
}

// syncMCP reconciles running servers with the configured set (hot
// reload): stopped-and-restarted for changed signatures, closed for
// removed ones, started for additions; failures degrade, never block.
func (s *AgentService) syncMCP() {
	s.mcpSyncMu.Lock() // 串行化：并发的 sync 为同名服务器起两个进程
	defer s.mcpSyncMu.Unlock()
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

	// 启动在锁外（握手最长 20s），成功后再入表。Close 后的在途握手直
	// 接关闭服务器——异步启动（构造期）可能在服务关闭后才完成握手，
	// 入表即进程泄漏。
	for _, p := range toStart {
		server, err := mcp.Start(context.Background(), p.name, p.cfg)
		if err != nil {
			log.Printf("mcp reload: %v", err)
			continue
		}
		server.Signature = p.sig
		s.mu.Lock()
		if s.mcpClosed {
			s.mu.Unlock()
			server.Close()
			continue
		}
		s.mcp = append(s.mcp, server)
		s.mu.Unlock()
	}
}

// mcpSignature serializes a config's identity: command, args, env（stdio）
// 或 url, headers（HTTP，spec 规则 69）。输出取 sha256——签名对值的变更
// 仍敏感（热重载照常触发），但签名本身读不出凭据（env/header 值可能
// 是 token，教义：不进日志不回显）。
func mcpSignature(cfg mcp.Config) string {
	keys := func(m map[string]string) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	var b strings.Builder
	b.WriteString(cfg.Command + "|")
	for _, a := range cfg.Args {
		b.WriteString(a + "|")
	}
	for _, k := range keys(cfg.Env) {
		b.WriteString(k + "=" + cfg.Env[k] + "|")
	}
	b.WriteString(cfg.URL + "|")
	for _, k := range keys(cfg.Headers) {
		b.WriteString(k + "=" + cfg.Headers[k] + "|")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// startMCP is syncMCP over an empty set (first run); kept as the
// constructor's entry point.
func (s *AgentService) startMCP() { s.syncMCP() }

// Close shuts down MCP servers and every session's background jobs.
func (s *AgentService) Close() {
	// 命名 goroutine 可能在写会话 meta——先等它落地（最长一次 15s 补
	// 全超时），否则退出竞态会让 TempDir 清理/退出路径看到半写状态。
	s.titleWG.Wait()
	// 索引异步落盘同理收束（spec 规则 71）——goroutine 活过 TempDir
	// 清理会复活文件（#160 同族教训）。
	s.idxWG.Wait()
	// sessionStart 钩子由 Close 收束（spec 规则 24）：外部命令可达 10s
	// 超时，退出前等它跑完，避免钩子进程活过服务。
	s.hookWG.Wait()
	// 会话记忆自动化（spec 规则 74）：收尾为有实质回合的会话提炼一
	// 次持久事实——在 provider/MCP 拆卸前发、落地后才继续（记忆是
	// 增益：任何失败记日志不阻塞退出）。
	s.fireAutoMemory()
	s.memWG.Wait()
	// sessionEnd 钩子对全部存活会话同步跑一次（观察者；退出前唯一机
	// 会），随后才拆工作区连接。
	for _, info := range s.mgr.List() {
		s.fireSessionEnd(info.ID, info.Workspace, "close")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpClosed = true
	for _, server := range s.mcp {
		server.Close()
	}
	s.mcp = nil
	for _, state := range s.states {
		state.Jobs.KillAll()
		state.Agents.CloseAll()
	}
	for _, close := range s.wsClose {
		close()
	}
	s.wsClose = map[string]func(){}
	// gopls daemon 全量收割（spec 规则 70）——不留孤儿进程过服务生命
	// 周期；下次查询按需重新拉起。
	lsp.Default.CloseAll()
}

// syncRedaction 把设置里的密钥喂给打码器（spec 规则 73）：活动 key、
// 全部剖面 key、OAuth 双 token——精确匹配零误报；开关随配置。key 轮
// 换后 Set 重置，旧值即失效。
func (s *AgentService) syncRedaction(cfg settings) {
	redact.SetEnabled(cfg.RedactSecrets == nil || *cfg.RedactSecrets)
	secrets := []string{cfg.APIKey}
	for _, p := range cfg.Profiles {
		secrets = append(secrets, p.APIKey)
	}
	secrets = append(secrets, cfg.OAuth.AccessToken, cfg.OAuth.RefreshToken)
	redact.Set(secrets...)
}

// rewire installs the current settings into the manager. Callers hold s.mu.
func (s *AgentService) rewire() {
	cfg := s.resolve()
	s.syncRedaction(cfg)
	// buildTools: see below (assigned before Wire).
	s.buildTools = func(workspace, sessionID string) []agent.Tool {
		s.mu.Lock()
		state, ok := s.states[sessionID]
		if !ok {
			state = tools.NewStates()
			s.states[sessionID] = state
		}
		// e2e provider 注入的持锁快照：嵌套循环（task/dispatch/
		// workflow/后台 agent）必须与主循环同一 provider 源——直连
		// providerFor 会让注入对子任务不生效、真实客户端带着空
		// key 出网。
		s.mu.Unlock()
		ov := s.providerOverride
		provider := func() ai.Provider {
			if ov != nil {
				return ov()
			}
			return s.providerFor(cfg)
		}
		model := cfg.Model
		question := s.question
		// 先拨号再建嵌套工具面：ssh:// 会话的子代理/工作流步骤经
		// ReadOnlyW 用 Remote 工作区（此前字符串重建的是本地面）。
		ws := s.workspaceFor(sessionID, workspace)
		roTools := tools.ReadOnlyW(ws)
		// allTools 在 task/dispatch 执行时才调用（闭包变量 all 此时已
		// 装配完毕）——白名单过滤需要完整会话工具面。
		var all []agent.Tool
		allTools := func() []agent.Tool { return all }
		// subtaskCfg 解析一次子任务装配：agentName 空走默认研究子代
		// 理（行为与既往一致）；命中 .zgo/agents 定义则用其 persona，
		// 工具面按白名单从会话工具集选取——白名单只收窄不放宽，词表
		// 外的名字忽略；edit 族继承主权限门（spec 规则 51）。
		subtaskCfg := func(agentName string) (agent.SubtaskConfig, error) {
			if agentName == "" {
				return agent.SubtaskConfig{
					Provider: provider(), Model: model,
					System: agent.SubtaskSystem(), Tools: roTools,
				}, nil
			}
			def, ok := agents.Find(agents.Discover(s.agentRoots(sessionID, workspace)), agentName)
			if !ok {
				names := agents.Names(agents.Discover(s.agentRoots(sessionID, workspace)))
				if len(names) == 0 {
					names = []string{"(none defined — add .zgo/agents/*.md)"}
				}
				return agent.SubtaskConfig{}, fmt.Errorf(
					"unknown agent %q; available: %s", agentName, strings.Join(names, ", "))
			}
			cfg := agent.SubtaskConfig{
				Provider: provider(), Model: model,
				System: def.System, Tools: roTools,
			}
			if len(def.Tools) > 0 {
				// 白名单只收窄不放宽：候选池 = 只读族 ∪ 会话工具集，
				// 但词表封闭（spec 规则 51）——task/dispatch/agent 等
				// 委派工具与 bash 不在词表内，防嵌套递归与直通执行；
				// 词表外的名字静默忽略，拼写错误的兜底是报错清单。
				pool := append(append([]agent.Tool{}, roTools...), allTools()...)
				cfg.Tools = filterToolsByName(pool, def.Tools)
				cfg.Permissions = permissionFor(s, sessionID)
			}
			return cfg, nil
		}
		task := tools.NewTask(func(ctx context.Context, prompt, agentName string) (agent.SubtaskResult, error) {
			// 子代理熔断（spec 规则 87）：硬预算闸对回合内 nested
			// loop 同样生效——启动前拒。
			if err := s.hardFuseErr(); err != nil {
				return agent.SubtaskResult{}, err
			}
			cfg, err := subtaskCfg(agentName)
			if err != nil {
				return agent.SubtaskResult{}, err
			}
			res, err := agent.RunSubtask(ctx, cfg, prompt)
			s.recordSubagentUsage(res.Usage) // 用量入当日记账（规则 87）
			return res, err
		})
		contextRead := contextReader(ws)
		// agentRunner 是后台子代理的运行器（spec 规则 50/51）：
		// prior 为空即全新任务，非空即续跑（历史由注册表提供）。
		agentRunner := func(ctx context.Context, id, prompt string, prior []ai.Message) (agent.SubtaskResult, []ai.Message, error) {
			// 升级提问（spec 规则 63，ZCode escalate）：后台研究代理
			// 遇到只有用户能答的问题时经会话提问通道升级——问题带
			// 代理标识前缀，答案回转录继续跑。
			agentAsk := tools.NewAsk(func(ctx context.Context, q agent.Question) (string, error) {
				return question(ctx, agent.Question{
					Text:    "[后台代理 " + id + "] " + q.Text,
					Options: q.Options,
				})
			})
			tools := append(append([]agent.Tool{}, roTools...), agentAsk)
			// 后台代理熔断（规则 87）：启动前拒——后台烧钱最狠且无
			// 回合边界。
			if ferr := s.hardFuseErr(); ferr != nil {
				return agent.SubtaskResult{}, nil, ferr
			}
			res, hist, err := agent.RunSubtaskSeeded(ctx, agent.SubtaskConfig{
				Provider: provider(),
				Model:    model,
				System:   agent.SubtaskSystem(),
				Tools:    tools,
			}, prior, prompt)
			s.recordSubagentUsage(res.Usage) // 用量入当日记账（规则 87）
			return res, hist, err
		}
		all = append(tools.AllW(ws, state, question), task,
			tools.NewMemoryTool(s.memoryFor(workspace)),
			tools.NewSkillTool(func() []skills.Root {
				return s.skillRoots(sessionID, workspace)
			}),
			// webfetch 与工作区无关，进程级共享（缓存跨会话）。
			tools.NewWebFetchTool(tools.WebFetcher),
			// 后台子代理三件套 + 续话（spec 规则 50/51）。
			tools.NewAgentTool(state.Agents, agentRunner),
			tools.NewAgentOutputTool(state.Agents),
			tools.NewAgentStopTool(state.Agents),
			tools.NewAgentSendTool(state.Agents, agentRunner))
		wfProvider := provider
		wfModel := cfg.Model
		wfPerms := permissionFor(s, sessionID)
		wfAsk := s.ask
		all = append(all, tools.NewDispatch(func(ctx context.Context, prompts []string, agentName string) []agent.SubtaskResult {
			if ferr := s.hardFuseErr(); ferr != nil { // 子代理熔断（规则 87）
				out := make([]agent.SubtaskResult, len(prompts))
				for i := range out {
					out[i] = agent.SubtaskResult{Text: "error: " + ferr.Error()}
				}
				return out
			}
			cfg, err := subtaskCfg(agentName)
			if err != nil {
				// 整批失败同型：每个条目一份错误文本（RunSubtasks 的
				// 单条失败语义——不拖垮别的 dispatch 调用）。
				out := make([]agent.SubtaskResult, len(prompts))
				for i := range out {
					out[i] = agent.SubtaskResult{Text: "error: " + err.Error()}
				}
				return out
			}
			results := agent.RunSubtasks(ctx, cfg, prompts)
			for _, r := range results { // 用量入当日记账（规则 87）
				s.recordSubagentUsage(r.Usage)
			}
			return results
		}))
		all = append(all, tools.NewWorkflow(func(ctx context.Context, req tools.WorkflowRequest) (tools.WorkflowOutcome, error) {
			return s.execWorkflow(ctx, sessionID, workspace, req,
				func(ctx context.Context, steps []agent.WorkflowStep, prefill map[int]agent.StepResult) ([]agent.StepResult, error) {
					return agent.RunWorkflow(ctx, agent.WorkflowConfig{
						Provider:    wfProvider(),
						Model:       wfModel,
						System:      prompt.SystemFS(workspace, contextRead),
						ReadOnly:    roTools,
						Editable:    tools.AllW(ws, state, question),
						Permissions: wfPerms,
						Ask:         wfAsk,
						Prefill:     prefill,
					}, steps)
				})
		}))
		for _, server := range s.mcpServers() {
			all = append(all, mcp.AgentTools(server)...)
		}
		// gopls 三工具（spec 规则 67/69）：仅本地工作区 + PATH 有
		// gopls 时声明（ssh:// 文件不在本地盘，wasm 无子进程）——
		// 模型可见面随环境稳定收窄，doctor 引导安装。查询走常驻
		// daemon（每根一进程、崩溃透明重启、失败退 CLI）。
		if !ws.IsRemote() && lsp.Available() {
			all = append(all, tools.NewGoplsTools(ws,
				func(ctx context.Context, dir string, args ...string) (string, error) {
					return lsp.DaemonRun(ctx, dir, lsp.QueryTimeout, args...)
				})...)
		}
		// 自定义工具（spec 规则 75）：.zgo/tools 命令模板包装成模
		// 型可调用工具——工作区层过信任门（tooldefRoots 内）；内
		// 置名优先，撞名的 def 不声明。
		if defs := tooldefs.Discover(s.tooldefRoots(sessionID, workspace)).Defs; len(defs) > 0 {
			names := map[string]bool{}
			for _, t := range all {
				names[t.Name()] = true
			}
			var free []tooldefs.Def
			for _, d := range defs {
				if !names[d.Name] {
					free = append(free, d)
				}
			}
			all = append(all, tools.NewCustomTools(free, ws, state.Jobs)...)
		}
		return all
	}
	s.mgr.Wire(agent.Wiring{
		Provider: func() ai.Provider {
			if s.providerOverride != nil {
				return s.providerOverride() // e2e 注入不套兜底——脚本按模型无关回放
			}
			return s.providerWithFailover(cfg, s.providerFor(cfg))
		},
		Model:    func() string { return cfg.Model },
		Thinking: func() ai.ThinkingLevel { return thinkingLevelOf(s.resolve().Thinking) },
		// 外部命令钩子来自设置文件（用户自有配置，非工作区），装配后
		// 随每个会话生效；配置为空时无钩子。
		Hooks: agent.HooksFromCommands(s.settings.Hooks),
		Tools: s.buildTools,
		System: func(workspace, sessionID string) string {
			sys := prompt.SystemFS(workspace, contextReader(s.workspaceFor(sessionID, workspace))) +
				s.memoryFor(workspace).Section()
			// 技能与子代理清单注入系统提示尾部（易变段语义：增删即时生
			// 效，不在缓存稳定前缀里）。
			return sys +
				skills.Section(skills.Discover(s.skillRoots(sessionID, workspace)), skills.DefaultMetaBudget) +
				agents.Section(agents.Discover(s.agentRoots(sessionID, workspace)), agents.DefaultMetaBudget)
		},
		Ask: s.ask,
		ProjectRules: func(workspace string) permission.Ruleset {
			// 工作区信任门（spec 规则 54）：未授信的本地工作区不加载
			// 其 .zgorules.json——克隆来的恶意仓库不能靠自带 allow
			// 规则给自己放权（提权面）。ssh:// 目标是用户显式拨号的
			// 远端，视为授信。
			if !s.workspaceTrusted(workspace) {
				return permission.Ruleset{}
			}
			return loadProjectRules(workspace)
		},
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
// providerFor 构建当前配置对应的 provider。OAuth 刷新经 refreshOAuth：
// 互斥 + 锁内重读（issue #136：并行子代理/DAG 同时撞过期会多路 Refresh
// 同一 refresh_token，服务端轮换后其余路 invalid_grant 回退旧 access
// token → 401 且不重试）。
// providerSnapshot 持锁快照 e2e 的 provider 注入（UseProviderForTesting）：
// 工具内嵌套循环（workflow/dispatch 子任务）必须与主循环同一 provider
// 源——直连 providerFor 会让注入对子任务不生效，真实客户端带着空 key
// 出网（首个 workflow e2e 抓到）。nil 即无注入。
func (s *AgentService) providerSnapshot() func() ai.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerOverride
}

// providerWithFailover 按设置把 provider 包成模型兜底链（spec 规则
// 55）；无 fallback 即原 provider。
func (s *AgentService) providerWithFailover(cfg settings, p ai.Provider) ai.Provider {
	if len(cfg.FallbackModels) == 0 {
		return p
	}
	models := append([]string{cfg.Model}, cfg.FallbackModels...)
	return ai.NewFailover(p, models)
}

func (s *AgentService) providerFor(cfg settings) ai.Provider {
	switch cfg.API {
	case "openai":
		return ai.NewOpenAI(cfg.BaseURL, cfg.APIKey)
	case "gemini":
		return ai.NewGemini(cfg.BaseURL, cfg.APIKey)
	default:
		client := ai.NewAnthropic(cfg.BaseURL, cfg.APIKey)
		if cfg.APIKey == "" && cfg.OAuth.AccessToken != "" {
			if cfg.OAuth.Expired() && cfg.OAuth.RefreshToken != "" {
				if fresh := s.refreshOAuth(cfg.OAuth); fresh.AccessToken != "" {
					client.OAuthToken = fresh.AccessToken
					return client
				}
			}
			client.OAuthToken = cfg.OAuth.AccessToken
		}
		return client
	}

}

// refreshOAuth 刷新订阅凭据（single-flight，issue #136）：互斥后重读
// settings——若 ExpiresAt 已被并发刷新就直接取新值（锁内重读是关键：
// 只串行化仍会各自拿旧值刷新）；否则 Refresh + persistOAuth。返回零值
// Token 表示刷新失败（调用方回退旧 token）。
func (s *AgentService) refreshOAuth(stale aiAuth.Token) aiAuth.Token {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if fresh := s.settings.OAuth; fresh.AccessToken != "" && !fresh.Expired() {
		return fresh // 别人已刷新，直接复用
	}
	fresh, err := aiAuth.Refresh(context.Background(), authTokenURL, stale.RefreshToken)
	if err != nil {
		return aiAuth.Token{}
	}
	s.persistOAuth(fresh)
	return fresh
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

// authTokenURL is the token endpoint (pi's constant). 测试可换端点。
var authTokenURL = aiAuth.TokenURL

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
		s.migrateProfilesLocked() // 首跑也建 default 剖面（无剖面态不做特判）
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading settings: %w", err)
	}
	if err := json.Unmarshal(data, &s.settings); err != nil {
		return fmt.Errorf("parsing settings: %w", err)
	}
	if s.migrateProfilesLocked() {
		// 旧版单剖面迁移结果即刻落盘（rewire 尚未装配，纯写文件）。
		if data, err := json.MarshalIndent(s.settings, "", "  "); err == nil {
			_ = os.WriteFile(s.path, data, 0o600)
		}
	}
	return nil
}

// migrateProfilesLocked 把旧版扁平单剖面迁移进 default 剖面；已有剖面
// 则只补齐缺失的 active。返回是否有变更。扁平字段语义不变——活动剖面
// 物化进扁平字段，providerFor/resolve 路径零感知。
func (s *AgentService) migrateProfilesLocked() bool {
	if len(s.settings.Profiles) > 0 {
		if s.settings.Active == "" {
			for name := range s.settings.Profiles {
				s.settings.Active = name
				break
			}
			return true
		}
		return false
	}
	cfg := s.settings
	s.settings.Profiles = map[string]profile{"default": {
		API: cfg.API, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
		Model: cfg.Model, Thinking: cfg.Thinking,
	}}
	s.settings.Active = "default"
	return true
}

// AddProfile 把当前活动配置注册为命名剖面（/provider add <名>）。名字
// 收敛为 ASCII 标识符——命令行解析可预测，map key 不含空白。
func (s *AgentService) AddProfile(name string) error {
	if !validProfileName(name) {
		return fmt.Errorf("invalid profile name %q (letters, digits, - and _)", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.settings
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]profile{}
	}
	if _, dup := cfg.Profiles[name]; dup {
		return fmt.Errorf("profile %q already exists", name)
	}
	cfg.Profiles[name] = profile{API: cfg.API, BaseURL: cfg.BaseURL,
		APIKey: cfg.APIKey, Model: cfg.Model, Thinking: cfg.Thinking}
	_, err := s.saveSettingsLocked(cfg)
	return err
}

// SwitchProfile 切换活动剖面：剖面值物化进扁平字段并落盘（下回合走新
// 剖面——rewire 即时生效）。未命中报错列出可用项自纠。
func (s *AgentService) SwitchProfile(name string) (SettingsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.settings.Profiles[name]
	if !ok {
		return s.view(), fmt.Errorf("unknown profile %q; available: %s",
			name, strings.Join(s.profileNamesLocked(), ", "))
	}
	cfg := s.settings
	cfg.API, cfg.BaseURL, cfg.APIKey = p.API, p.BaseURL, p.APIKey
	cfg.Model, cfg.Thinking, cfg.Active = p.Model, p.Thinking, name
	return s.saveSettingsLocked(cfg)
}

// profileNamesLocked 列出剖面名（稳定序；调用方持锁）。
func (s *AgentService) profileNamesLocked() []string {
	names := make([]string, 0, len(s.settings.Profiles))
	for n := range s.settings.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// validProfileName：非空、限 ASCII 字母/数字/-/_。
func validProfileName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// view projects the resolved settings for the UI.
func (s *AgentService) view() SettingsView {
	cfg := s.resolve()
	v := SettingsView{BaseURL: cfg.BaseURL, Model: cfg.Model, API: cfg.API,
		Thinking: cfg.Thinking, HasAPIKey: cfg.APIKey != "",
		Profile: s.settings.Active, Profiles: s.profileNamesLocked()}
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
	// 保存同步回活动剖面存储（剖面与扁平字段永远一致——切换回来看到
	// 的就是离开时的样子）。
	if cfg.Active != "" && cfg.Profiles != nil {
		if _, ok := cfg.Profiles[cfg.Active]; ok {
			p := cfg.Profiles[cfg.Active]
			p.API, p.BaseURL, p.Model, p.Thinking = cfg.API, cfg.BaseURL, cfg.Model, cfg.Thinking
			if apiKey != "" {
				p.APIKey = apiKey // 空参保留旧 key（同扁平语义）
			}
			cfg.Profiles[cfg.Active] = p
		}
	}
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
		// notification 钩子（spec 规则 68）：即将阻塞等待用户——headless/
		// TUI 接系统通知的挂点；观察者语义，失败只记日志。
		s.mu.Lock()
		notify := s.settings.Hooks.Notification
		s.mu.Unlock()
		agent.NotificationHooks(notify, map[string]any{
			"event": "notification", "sessionId": req.SessionID,
			"workspace": req.Workspace, "tool": req.Tool, "reason": req.Reason,
		})
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
	info := sess.Info()
	// sessionStart 钩子异步跑（观察者，spec 规则 24）：外部命令可达
	// 10s 超时，不能拖住会话打开；hookWG 让 Close 收束它。
	if hooks := s.hooksSnapshot().SessionStart; len(hooks) > 0 {
		s.hookWG.Add(1)
		go func() {
			defer s.hookWG.Done()
			agent.SessionStartHooks(hooks, map[string]any{
				"sessionId": info.ID, "workspace": info.Workspace,
				"title": info.Title,
			})
		}()
	}
	return info, nil
}

// HasSession reports whether the id (or its unique prefix) names a
// session; ResolveSession maps a unique prefix to the full id.
func (s *AgentService) HasSession(idOrPrefix string) bool {
	_, ok := s.ResolveSession(idOrPrefix)
	return ok
}

// ContinueSession picks the workspace's most recently modified session
// （终端 --continue：同工作区最近会话；无候选返回 false，调用方退回新
// 建——首用不报错）.
func (s *AgentService) ContinueSession(workspace string) (agent.SessionInfo, bool) {
	var best agent.SessionInfo
	found := false
	for _, info := range s.ListSessions() {
		if info.Workspace != workspace || info.Archived {
			continue // 归档会话不参与 -c 恢复（spec 规则 82）
		}
		if !found || info.UpdatedAt.After(best.UpdatedAt) {
			best, found = info, true
		}
	}
	return best, found
}

// ResolveSession maps a unique id prefix to the full session id — CLI
// ergonomics（时间戳前缀早早唯一，敲 15 位足够）.
func (s *AgentService) ResolveSession(idOrPrefix string) (string, bool) {
	if idOrPrefix == "" {
		return "", false
	}
	sessions := s.ListSessions()
	var match string
	for _, info := range sessions {
		if info.ID == idOrPrefix {
			return info.ID, true
		}
		if strings.HasPrefix(info.ID, idOrPrefix) {
			if match != "" {
				return "", false // 前缀歧义
			}
			match = info.ID
		}
	}
	return match, match != ""
}

// ListSessions returns the session index, newest first.
func (s *AgentService) ListSessions() []agent.SessionInfo {
	return s.mgr.List()
}

// DeleteSession removes a session (its background jobs stop first).
func (s *AgentService) DeleteSession(id string) error {
	var ws string
	if sess, ok := s.mgr.Get(id); ok {
		ws = sess.Info().Workspace
	}
	s.mu.Lock()
	if state, ok := s.states[id]; ok {
		state.Jobs.KillAll()
		state.Agents.CloseAll() // 后台子代理随会话清光（spec 规则 50）
		delete(s.states, id)
	}
	if close, ok := s.wsClose[id]; ok {
		close()
		delete(s.wsClose, id)
	}
	delete(s.wsCache, id)
	s.mu.Unlock()
	if err := s.mgr.Delete(id); err != nil {
		return err
	}
	// 检查点 refs 随会话清光（issue #224：活 ref 钉住 git 对象——不清
	// 是硬泄漏；ws 已在删 state 前取出，ssh:// 与非仓库静默无操作）；
	// 文件快照库同清（spec 规则 83）。
	if ws != "" && !strings.HasPrefix(ws, "ssh://") {
		if err := checkpoint.RemoveAll(ws, id); err != nil {
			log.Printf("removing checkpoint refs for %s: %v", id, err)
		}
	}
	if err := checkpoint.SnapshotRemoveAll(s.dir(), id); err != nil {
		log.Printf("removing snapshots for %s: %v", id, err)
	}
	s.fireSessionEnd(id, ws, "delete")
	return nil
}

// fireSessionEnd runs sessionEnd hooks（观察者，spec 规则 24）.
func (s *AgentService) fireSessionEnd(id, workspace, reason string) {
	hooks := s.hooksSnapshot().SessionEnd
	if len(hooks) == 0 {
		return
	}
	agent.SessionEndHooks(hooks, map[string]any{
		"sessionId": id, "workspace": workspace, "reason": reason,
	})
}

// hooksSnapshot reads the configured command hooks under lock.
func (s *AgentService) hooksSnapshot() agent.CommandHooks {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Hooks
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
	// 硬预算熔断（spec 规则 85）：今日已计价 token 达预算即拒新回合
	//（明确报错、不进转录——止损闸在回合开始前；cron 调度同闸）。
	if err := s.hardFuseErr(); err != nil {
		return err
	}
	// 回合开始前快照工作区（本地 git 仓库时）：seq = 新用户消息的序号
	//（现有用户消息数），回滚按序号定位。
	s.captureCheckpoint(sess, userTurnCount(sess.Messages()))
	before := sess.UsageTotal()
	text, media = s.expandMentions(sessionID, text, media)
	err := sess.ChatMedia(text, media, emit)
	if err == nil {
		// 每日预算记账（spec 规则 35）：回合增量折算入当日记账，跨阈
		// 值发一次通知（软预算——不阻断）。
		s.recordDailyUsage(before, sess.UsageTotal(), emit)
		// 首个回合成功结束后尝试自动命名（规则 21）：等值守卫在内部，
		// 改过名的会话这里直接空手而归；WaitGroup 让 Close 收束它。
		s.titleWG.Add(1)
		go func() {
			defer s.titleWG.Done()
			s.autoTitle(sessionID)
		}()
	}
	return err
}

// autoTitle generates a short session title after the first exchange
// (ZCode's auto-title): one small completion through the session's
// provider, persisted via the existing Rename path. Failures log and
// keep the default title——标题是化妆性的，绝不影响会话本身。
func (s *AgentService) autoTitle(sessionID string) {
	if !s.autoTitleOn {
		return
	}
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return
	}
	userText, assistantText, eligible := sess.AutoTitleContext()
	if !eligible {
		return
	}
	cfg := s.resolve()
	// providerOverride 持锁快照：命名 goroutine 活得比 Chat 调用久，
	// 裸读会与 UseProviderForTesting 的持锁写竞态（-race 在 CI 上逮过）。
	s.mu.Lock()
	override := s.providerOverride
	s.mu.Unlock()
	p := func() ai.Provider {
		if override != nil {
			return override() // e2e 注入（快照时点的注入者）
		}
		return s.providerFor(cfg)
	}
	title := generateTitle(cfg, p(), userText, assistantText)
	if title == "" {
		return
	}
	if err := sess.Rename(title); err != nil {
		log.Printf("auto-title %s: %v", sessionID, err)
	}
}

// titleMaxRunes bounds a generated title（titleFrom 的默认截断同为 40）。
const titleMaxRunes = 40

// generateTitle runs the small completion and sanitizes its reply: single
// line, no wrapping quotes, ≤40 runes. Empty on any failure.
func generateTitle(cfg settings, p ai.Provider, userText, assistantText string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sys := "You title coding-agent sessions. Reply with ONLY the title: a " +
		"single line of at most 24 characters, no quotes, in the user's " +
		"language, describing the task."
	prompt := "User: " + excerptRunes(userText, 400)
	if assistantText != "" {
		prompt += "\nAssistant: " + excerptRunes(assistantText, 400)
	}
	prompt += "\nTitle:"
	req := ai.Request{
		Model:     cfg.Model,
		System:    sys,
		Messages:  []ai.Message{{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}},
		MaxTokens: 48,
	}
	var title string
	for ev := range p.Stream(ctx, req) {
		if ev.Kind == ai.EvTextDelta {
			title += ev.TextDelta
		}
		if ev.Kind == ai.EvMessageEnd && ev.StopReason == ai.StopError {
			return ""
		}
	}
	title = strings.TrimSpace(strings.Split(title, "\n")[0])
	title = strings.Trim(title, "\"'“”‘’「」『』")
	if r := []rune(title); len(r) > titleMaxRunes {
		title = string(r[:titleMaxRunes])
	}
	return title
}

// excerptRunes takes at most n runes of s.
func excerptRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// captureCheckpoint snapshots the workspace before a turn. SSH-remote
// degrades to conversation-only rewind; 非 git 本地工作区走文件快照
// （spec 规则 83）——超限降级回既往行为。
func (s *AgentService) captureCheckpoint(sess *agent.Session, seq int) {
	info := sess.Info()
	if strings.HasPrefix(info.Workspace, "ssh://") {
		// 远程快照（spec 规则 84）：ssh:// 此前完全降级——经拨号后的
		// RemoteFS 拉整树进本地快照库；超限同样降级。
		ws := s.workspaceFor(info.ID, info.Workspace)
		if ws.Remote == nil {
			return
		}
		if err := checkpoint.SnapshotCaptureRemote(s.dir(), ws.Remote, info.ID, seq); err != nil &&
			!errors.Is(err, checkpoint.ErrTooLarge) {
			log.Printf("remote snapshot capture: %v", err)
		}
		return
	}
	if err := checkpoint.Capture(info.Workspace, info.ID, seq); err != nil {
		if !errors.Is(err, checkpoint.ErrNotARepo) {
			log.Printf("checkpoint capture: %v", err)
			return
		}
		if serr := checkpoint.SnapshotCapture(s.dir(), info.Workspace, info.ID, seq); serr != nil &&
			!errors.Is(serr, checkpoint.ErrTooLarge) {
			log.Printf("snapshot capture: %v", serr)
		} // ErrTooLarge：大目录维持既往「仅回退转录」——不因快照失败阻塞回合
	}
}

// restoreCheckpoint rolls the workspace back to a turn's start. 非仓库按
// 仅回退转录降级（返回 nil）；仓库里检查点被保留窗口修剪则明确报错
// 中止——调用方不得回退转录（issue #152：静默半回退让模型拿旧假设改
// 新文件；issue #110 同族：restore 失败必须中止）。
func (s *AgentService) restoreCheckpoint(sess *agent.Session, seq int) error {
	info := sess.Info()
	if strings.HasPrefix(info.Workspace, "ssh://") {
		// 远程快照（spec 规则 84）：错误分层与本地同源——旧会话降级、
		// 修剪中止。
		ws := s.workspaceFor(info.ID, info.Workspace)
		if ws.Remote == nil {
			return nil
		}
		rerr := checkpoint.SnapshotRestoreRemote(s.dir(), ws.Remote, info.ID, seq)
		if errors.Is(rerr, checkpoint.ErrSnapshotMissing) {
			return nil
		}
		if errors.Is(rerr, checkpoint.ErrSnapshotPruned) {
			return fmt.Errorf("该回合的快照已修剪（保留最近 %d 回合），文件未回退——为避免对话与文件分裂已中止回退；可回退更近的回合", checkpoint.Retention)
		}
		if rerr != nil {
			return fmt.Errorf("restoring remote snapshot: %w", rerr)
		}
		return nil
	}
	err := checkpoint.Restore(info.Workspace, info.ID, seq)
	if errors.Is(err, checkpoint.ErrNotARepo) {
		// 文件快照路径（spec 规则 83）：旧会话无快照 → 降级仅回退转
		// 录；有快照但被修剪 → 中止（对齐仓库内语义，issue #152）。
		serr := checkpoint.SnapshotRestore(s.dir(), info.Workspace, info.ID, seq)
		if errors.Is(serr, checkpoint.ErrSnapshotMissing) {
			return nil
		}
		if errors.Is(serr, checkpoint.ErrSnapshotPruned) {
			return fmt.Errorf("该回合的快照已修剪（保留最近 %d 回合），文件未回退——为避免对话与文件分裂已中止回退；可回退更近的回合", checkpoint.Retention)
		}
		if serr != nil {
			return fmt.Errorf("restoring workspace snapshot: %w", serr)
		}
		return nil
	}
	if errors.Is(err, checkpoint.ErrCheckpointPruned) {
		return fmt.Errorf("该回合的检查点已修剪（保留最近 %d 回合），文件未回退——为避免对话与文件分裂已中止回退；可回退更近的回合，或用 git 手动恢复", checkpoint.Retention)
	}
	if err != nil {
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

// RewindTo rolls the session back before its nth user turn (1-based):
// the workspace restores to that turn's checkpoint and the transcript
// drops everything from that turn on（任意回合版 RewindLatest——fork 面向
// "试另一条路"，本方法面向"这条路走错了"）.
func (s *AgentService) RewindTo(sessionID string, turn int) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	if sess.Running() {
		return fmt.Errorf("a run is already in progress")
	}
	msgs := sess.Messages()
	total := userTurnCount(msgs)
	if turn < 1 || turn > total {
		return fmt.Errorf("turn %d out of range (1..%d)", turn, total)
	}
	if err := s.restoreCheckpoint(sess, turn-1); err != nil {
		return err
	}
	return sess.Rewind(userTurnIndex(msgs, turn))
}

// userTurnIndex maps a 1-based turn ordinal to its transcript index.
func userTurnIndex(msgs []ai.Message, turn int) int {
	seen := 0
	for i, m := range msgs {
		if m.Role == ai.RoleUser {
			seen++
			if seen == turn {
				return i
			}
		}
	}
	return len(msgs)
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
// preCompact 钩子先跑（spec 规则 24）：非零退出阻止压缩并回带原因。
func (s *AgentService) CompactSession(id string) error {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	if hooks := s.hooksSnapshot().PreCompact; len(hooks) > 0 {
		if blocked, reason := agent.PreCompactHooks(hooks, map[string]any{
			"sessionId": id, "workspace": sess.Info().Workspace,
			"messages": len(sess.Messages()), "trigger": "manual",
		}); blocked {
			if reason == "" {
				reason = "钩子未说明原因"
			}
			return fmt.Errorf("压缩被钩子阻止：%s", reason)
		}
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

// workspaceTrusted 报告工作区是否受信（spec 规则 54）：ssh:// 目标是
// 用户显式拨号的远端——视为授信；本地路径查授信表。
func (s *AgentService) workspaceTrusted(workspace string) bool {
	if strings.HasPrefix(workspace, "ssh://") {
		return true
	}
	return s.trustStore.IsTrusted(workspace)
}

// IsWorkspaceTrusted 是表面查询（终端/桌面的未授信提示用）。
func (s *AgentService) IsWorkspaceTrusted(workspace string) bool {
	return s.workspaceTrusted(workspace)
}

// TrustWorkspace 授信并即时生效：本会话的项目规则重载（若已在跑），
// 技能/命令的工作区层随下次发现自然生效。
func (s *AgentService) TrustWorkspace(workspace, via string) error {
	if err := s.trustStore.Grant(workspace, via); err != nil {
		return err
	}
	rules := loadProjectRules(workspace)
	for _, info := range s.mgr.List() {
		if trust.Normalize(info.Workspace) == trust.Normalize(workspace) {
			if ssn, ok := s.mgr.Get(info.ID); ok {
				ssn.ApplyProjectRules(rules)
			}
		}
	}
	return nil
}

// TrustSession 授信当前会话的工作区（/trust 用）。
func (s *AgentService) TrustSession(sessionID, via string) error {
	ssn, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("会话不存在")
	}
	return s.TrustWorkspace(ssn.Info().Workspace, via)
}

// RevokeWorkspace 撤销授信（在跑会话的项目规则即时清空——授信回退
// 生效，spec 规则 54）。
func (s *AgentService) RevokeWorkspace(workspace string) error {
	if err := s.trustStore.Revoke(workspace); err != nil {
		return err
	}
	for _, info := range s.mgr.List() {
		if trust.Normalize(info.Workspace) == trust.Normalize(workspace) {
			if ssn, ok := s.mgr.Get(info.ID); ok {
				ssn.ApplyProjectRules(permission.Ruleset{})
			}
		}
	}
	return nil
}

// TrustList 列出授信表。
func (s *AgentService) TrustList() []trust.Entry { return s.trustStore.List() }

// contextReader returns the remote context-file reader (root-relative),
// or nil for local workspaces——远程会话的项目上下文经网线读取（此前读
// 本地路径必然落空、静默丢失）。
func contextReader(ws tools.Workspace) func(string) ([]byte, error) {
	if !ws.IsRemote() {
		return nil
	}
	return ws.Remote.ReadFile
}

// skillsBaseDir 是工作区内的技能根（相对形态，远端同享）。
const skillsBaseDir = ".zgo/skills"

// skillRoots 返回技能发现根（工作区在前——同名工作区覆盖全局，与自定
// 义命令同约定）：工作区 .zgo/skills（远程经网线）+ 全局 ~/.zgo/skills
// （本地）。每次调用重新取 workspaceFor（拨号缓存），技能增删即时生效。
func (s *AgentService) skillRoots(sessionID, workspace string) []skills.Root {
	ws := s.workspaceFor(sessionID, workspace)
	var roots []skills.Root
	// 信任门（spec 规则 54）：未授信的本地工作区不带其技能层（提示
	// 注入面）；全局层不受影响。
	if !ws.IsRemote() && !s.workspaceTrusted(workspace) {
		// 未授信：全局层 + 内置层（内置随二进制发行，不是注入面——
		// 信任门只挡工作区层）。
		if home, err := os.UserHomeDir(); err == nil {
			return []skills.Root{{
				Base:  filepath.Join(home, ".zgo", "skills"),
				Scope: skills.ScopeUser, FS: skills.LocalFS{},
			}, skills.BuiltinRoot()}
		}
		return []skills.Root{skills.BuiltinRoot()}
	}
	if ws.IsRemote() {
		roots = append(roots, skills.Root{
			Base: skillsBaseDir, Scope: skills.ScopeProject,
			FS: remoteSkillsFS{ws.Remote},
		})
	} else {
		roots = append(roots, skills.Root{
			Base:  filepath.Join(ws.Root, filepath.FromSlash(skillsBaseDir)),
			Scope: skills.ScopeProject, FS: skills.LocalFS{},
		})
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, skills.Root{
			Base:  filepath.Join(home, ".zgo", "skills"),
			Scope: skills.ScopeUser, FS: skills.LocalFS{},
		})
	}
	// 内置技能层（spec 规则 72）：末位最低优先——同名被工作区/全局
	// 覆盖；嵌入内容不读盘，未授信同样可用。
	roots = append(roots, skills.BuiltinRoot())
	return roots
}

// Skills 列出工作区的可用技能（/skills 终端命令用；与 agent 的发现
// 同一根源——工作区层经 openWorkspace 解析目标，远程走网线）。
func (s *AgentService) Skills(workspace string) skills.Outcome {
	if !s.workspaceTrusted(workspace) {
		// 信任门与 skillRoots 同款（spec 规则 54）：列表面不显示未
		// 授信工作区的技能层。
		var roots []skills.Root
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, skills.Root{
				Base:  filepath.Join(home, ".zgo", "skills"),
				Scope: skills.ScopeUser, FS: skills.LocalFS{},
			})
		}
		roots = append(roots, skills.BuiltinRoot())
		return skills.Discover(roots)
	}
	project := skills.Root{
		Base:  filepath.Join(workspace, filepath.FromSlash(skillsBaseDir)),
		Scope: skills.ScopeProject, FS: skills.LocalFS{},
	}
	if ws, _, err := s.openWorkspace(workspace); err == nil {
		if ws.IsRemote() {
			project = skills.Root{Base: skillsBaseDir, Scope: skills.ScopeProject,
				FS: remoteSkillsFS{ws.Remote}}
		} else {
			project.Base = filepath.Join(ws.Root, filepath.FromSlash(skillsBaseDir))
		}
	}
	roots := []skills.Root{project}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, skills.Root{
			Base:  filepath.Join(home, ".zgo", "skills"),
			Scope: skills.ScopeUser, FS: skills.LocalFS{},
		})
	}
	roots = append(roots, skills.BuiltinRoot())
	return skills.Discover(roots)
}

// remoteSkillsFS 把 tools.RemoteFS 适配成 skills.FS（路径根相对、斜杠
// 形态——与远端文件工具同源）。
type remoteSkillsFS struct{ r tools.RemoteFS }

// Join implements skills.FS.
func (f remoteSkillsFS) Join(parts ...string) string { return path.Join(parts...) }

// ReadFile implements skills.FS.
func (f remoteSkillsFS) ReadFile(name string) ([]byte, error) { return f.r.ReadFile(name) }

// ReadDir implements skills.FS.
func (f remoteSkillsFS) ReadDir(dir string) ([]skills.DirEntry, error) {
	es, err := f.r.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]skills.DirEntry, len(es))
	for i, e := range es {
		out[i] = skills.DirEntry{Name: e.Name, IsDir: e.IsDir}
	}
	return out, nil
}

// agentsBaseDir 是工作区内的子代理定义根（相对形态，远端同享）。
const agentsBaseDir = ".zgo/agents"

// agentRoots 返回子代理定义发现根（工作区在前——同名工作区覆盖全局，
// 与技能同约定）：工作区 .zgo/agents（远程经网线）+ 全局 ~/.zgo/agents
// （本地）。每次调用重新取 workspaceFor（拨号缓存），定义增删即时生效。
func (s *AgentService) agentRoots(sessionID, workspace string) []agents.Root {
	ws := s.workspaceFor(sessionID, workspace)
	var roots []agents.Root
	if ws.IsRemote() {
		roots = append(roots, agents.Root{
			Base: agentsBaseDir, Scope: agents.ScopeProject,
			FS: remoteAgentsFS{ws.Remote},
		})
	} else {
		roots = append(roots, agents.Root{
			Base:  filepath.Join(ws.Root, filepath.FromSlash(agentsBaseDir)),
			Scope: agents.ScopeProject, FS: agents.LocalFS{},
		})
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, agents.Root{
			Base:  filepath.Join(home, ".zgo", "agents"),
			Scope: agents.ScopeUser, FS: agents.LocalFS{},
		})
	}
	return roots
}

// remoteAgentsFS 把 RemoteFS 适配为 agents.FS（与 remoteSkillsFS 同型）。
type remoteAgentsFS struct{ r tools.RemoteFS }

// Join implements agents.FS.
func (f remoteAgentsFS) Join(parts ...string) string { return path.Join(parts...) }

// ReadFile implements agents.FS.
func (f remoteAgentsFS) ReadFile(name string) ([]byte, error) { return f.r.ReadFile(name) }

// ReadDir implements agents.FS.
func (f remoteAgentsFS) ReadDir(dir string) ([]agents.DirEntry, error) {
	es, err := f.r.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]agents.DirEntry, len(es))
	for i, e := range es {
		out[i] = agents.DirEntry{Name: e.Name, IsDir: e.IsDir}
	}
	return out, nil
}

// subagentToolVocab 是子代理定义白名单的封闭词表（spec 规则 51）：委派
// 工具（task/dispatch/agent 族/workflow）与 bash 不在内——防嵌套递归与
// 直通执行；词表外的白名单名字忽略。
var subagentToolVocab = map[string]bool{
	"read": true, "grep": true, "glob": true, "ls": true,
	"edit": true, "write": true, "patch": true, "todo": true,
}

// filterToolsByName 按白名单过滤候选池（只收窄：白名单 ∩ 词表）。
func filterToolsByName(pool []agent.Tool, names []string) []agent.Tool {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []agent.Tool
	for _, t := range pool {
		if subagentToolVocab[t.Name()] && want[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

// execWorkflow 是 workflow 工具的执行面（spec 规则 48）：解析三种调用
// 形态（临场步骤/按名/续跑），开跑即落盘运行记录，终态回填；saveAs 在
// 校验通过后把定义持久化（运行失败不影响保存——定义本身仍有效）。
func (s *AgentService) execWorkflow(ctx context.Context, sessionID, workspace string,
	req tools.WorkflowRequest, run func(ctx context.Context, steps []agent.WorkflowStep, prefill map[int]agent.StepResult) ([]agent.StepResult, error)) (tools.WorkflowOutcome, error) {
	var outcome tools.WorkflowOutcome
	steps := req.Steps
	name := ""
	var prefill map[int]agent.StepResult
	switch {
	case len(req.Steps) > 0:
		if _, err := agent.ValidateWorkflowSteps(req.Steps); err != nil {
			return outcome, err
		}
	case req.Run != "":
		def, err := s.wfStore.Get(req.Run)
		if err != nil {
			return outcome, err
		}
		steps, name = def.Steps, def.Name
	case req.Resume != "":
		prev, err := s.wfStore.GetRun(req.Resume)
		if err != nil {
			return outcome, err
		}
		if prev.Status == "running" || prev.Status == "completed" {
			if prev.Status == "completed" {
				return outcome, fmt.Errorf("运行 %s 已完成，无需续跑", req.Resume)
			}
		}
		if prev.Name != "" {
			if def, err := s.wfStore.Get(prev.Name); err == nil {
				steps, name = def.Steps, def.Name // 定义优先（记录里的步骤表无 After 语义漂移防护）
			} else {
				steps = prev.StepsOf()
			}
		} else {
			steps = prev.StepsOf()
		}
		prefill = prev.PrefillOf(steps)
	default:
		return outcome, fmt.Errorf("workflow needs steps, run or resume")
	}
	if _, err := agent.ValidateWorkflowSteps(steps); err != nil {
		return outcome, err
	}
	rec, err := s.wfStore.StartRun(name, workspace, steps)
	if err != nil {
		return outcome, err
	}
	outcome.RunID = rec.ID
	results, runErr := run(ctx, steps, prefill)
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	_ = s.wfStore.FinishRun(rec, results, status, errText(runErr))
	outcome.Results = results
	if req.SaveAs != "" {
		if err := s.wfStore.Save(workflow.Definition{Name: req.SaveAs, Steps: steps}); err != nil {
			runErr = fmt.Errorf("%v（工作流已执行，但保存失败）", err)
		} else {
			outcome.SavedAs = req.SaveAs
		}
	}
	return outcome, runErr
}

// WorkflowStore 暴露工作流存储面（CLI 只读视图用）。
func (s *AgentService) WorkflowStore() *workflow.Store { return s.wfStore }

// errText 安全取错误文本（nil → ""）。
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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

// mcpServers snapshots the launched servers（拷贝出表——异步启动后
// syncMCP 随时会写表，裸读曾是竞态，CI -race 在 TestMCPPrompts 逮
// 过）。自身持锁；已持锁的调用方用 mcpServersLocked。
func (s *AgentService) mcpServers() []*mcp.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpServersLocked()
}

// mcpServersLocked 是 mcpServers 的持锁形态（调用方持有 s.mu）。
func (s *AgentService) mcpServersLocked() []*mcp.Server {
	return append([]*mcp.Server(nil), s.mcp...)
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

// UsageSummary is the /usage surface's data（spec 规则 25）.
type UsageSummary struct {
	SessionID string
	Title     string
	Usage     ai.Usage
	Turns     int // 用户回合数
	// Today/TodayBudget 是当日记账与预算（0=不限，spec 规则 35）。
	Today       int
	TodayBudget int
}

// UsageSummaryOf collects a session's usage accounting for display.
func (s *AgentService) UsageSummaryOf(id string) (UsageSummary, error) {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return UsageSummary{}, fmt.Errorf("no session %s", id)
	}
	info := sess.Info()
	today, budget := s.DailyUsage()
	return UsageSummary{
		SessionID:   info.ID,
		Title:       info.Title,
		Usage:       sess.UsageTotal(),
		Turns:       userTurnCount(sess.Messages()),
		Today:       today,
		TodayBudget: budget,
	}, nil
}

// FormatUsage renders a summary as the shared display block（/usage 与
// zgo usage 同一张表）.
func FormatUsage(u UsageSummary) string {
	hit := ""
	total := u.Usage.InputTokens + u.Usage.CacheReadTokens
	if total > 0 {
		pct := u.Usage.CacheReadTokens * 100 / total
		hit = fmt.Sprintf("（前缀缓存命中 %d%%）", pct)
	}
	today := fmt.Sprintf("今日        %d tokens", u.Today)
	if u.TodayBudget > 0 {
		today += fmt.Sprintf("（预算 %d）", u.TodayBudget)
	}
	return fmt.Sprintf("会话 %s · %s\n回合 %d\n↑ 输入      %d tokens\n↓ 输出      %d tokens\n⤒ 缓存读    %d tokens %s\n⤓ 缓存写    %d tokens\n%s",
		shortIDOf(u.SessionID), u.Title, u.Turns,
		u.Usage.InputTokens, u.Usage.OutputTokens,
		u.Usage.CacheReadTokens, hit, u.Usage.CacheWriteTokens, today)
}

// shortIDOf keeps the formatter self-contained（cmd 包另有同名助手）.
func shortIDOf(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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

// AgentJobStates 返回会话的后台子代理快照（e2e 等待终态、后续 UI 面
// 同源）。
func (s *AgentService) AgentJobStates(sessionID string) []tools.AgentJobView {
	s.mu.Lock()
	state, ok := s.states[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return state.Agents.Snapshots()
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

// AppendForTesting appends a raw message to a session's transcript
// （sessions-CLI 等 e2e 的数据铺垫；生产路径一律经 Chat 家族）。
func (s *AgentService) AppendForTesting(sessionID string, m ai.Message) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("no session %s", sessionID)
	}
	return sess.Append(m)
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

// DisableAutoTitleForTesting turns off auto-titling（e2e 脚本 provider 按
// 请求序号回放，额外的命名请求会吃掉脚本回合、破坏计数断言；自动命
// 名自身有专测）。
func (s *AgentService) DisableAutoTitleForTesting() {
	s.mu.Lock()
	s.autoTitleOn = false
	s.mu.Unlock()
}

// Abort cancels a session's running loop.
func (s *AgentService) Abort(sessionID string) {
	if sess, ok := s.mgr.Get(sessionID); ok {
		sess.Abort()
	}
}

// MCPPromptEntry is one listed server prompt（/mcp 的列表项）.
type MCPPromptEntry struct {
	Server string
	Prompt mcp.PromptDef
}

// MCPPrompts lists every running server's prompts.
func (s *AgentService) MCPPrompts() []MCPPromptEntry {
	var out []MCPPromptEntry
	for _, srv := range s.mcpServers() {
		for _, p := range srv.Prompts() {
			out = append(out, MCPPromptEntry{Server: srv.Name, Prompt: p})
		}
	}
	return out
}

// MCPGetPrompt expands one prompt（spec 规则 28）：k=v 实参经 prompts/get
// 展开为可直接发送的文本。
func (s *AgentService) MCPGetPrompt(server, name string, args map[string]string) (string, error) {
	for _, srv := range s.mcpServers() {
		if srv.Name != server {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := srv.GetPrompt(ctx, name, args)
		if err != nil {
			return "", err
		}
		if text := res.PromptText(); text != "" {
			return text, nil
		}
		return "", fmt.Errorf("prompt %s.%s 展开为空", server, name)
	}
	return "", fmt.Errorf("没有 MCP 服务器 %q", server)
}

// LastUserText returns the session's last user message text（/retry 的
// 重跑输入；空串表示没有可重跑的回合）.
func (s *AgentService) LastUserText(sessionID string) (string, error) {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("no session %s", sessionID)
	}
	msgs := sess.Messages()
	if i := agent.LastUserIndex(msgs); i >= 0 {
		return msgs[i].Text(), nil
	}
	return "", nil
}

// MCPResourceEntry is one listed server resource（/mcp 的资源项）.
type MCPResourceEntry struct {
	Server   string
	Resource mcp.ResourceDef
}

// MCPResources lists every running server's resources（spec 规则 31）.
func (s *AgentService) MCPResources() []MCPResourceEntry {
	var out []MCPResourceEntry
	for _, srv := range s.mcpServers() {
		for _, r := range srv.Resources() {
			out = append(out, MCPResourceEntry{Server: srv.Name, Resource: r})
		}
	}
	return out
}

// MCPReadResource reads one resource's text（resources/read）.
func (s *AgentService) MCPReadResource(server, uri string) (string, error) {
	for _, srv := range s.mcpServers() {
		if srv.Name != server {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := srv.ReadResource(ctx, uri)
		if err != nil {
			return "", err
		}
		if res.Text == "" {
			return "", fmt.Errorf("资源 %s 无文本内容（mimeType %s，blob 类资源暂不支持）", uri, res.MimeType)
		}
		return capResourceText(res.Text), nil
	}
	return "", fmt.Errorf("没有 MCP 服务器 %q", server)
}

// mcpResourceCapRunes 封顶资源文本（rune 计，对齐 @提及/stdin 的 60K
// 口径，issue #194：MCP 资源的典型形态恰是大文本——日志、数据集切片；
// 无界直入消息即上下文爆炸 + JSONL 行超 8MB 读取上限的家族第三例）。
const mcpResourceCapRunes = 60 << 10

// capResourceText 截断超限资源并留可读标注（原长度可见，模型可改用别
// 的途径取全文）。
func capResourceText(text string) string {
	r := []rune(text)
	if len(r) <= mcpResourceCapRunes {
		return text
	}
	return string(r[:mcpResourceCapRunes]) +
		fmt.Sprintf("\n…（资源超长，已截断，原 %d 字符）", len(r))
}

// recordDailyUsage 把本回合的用量增量折算入当日记账，跨阈值发一次通
// 知（80%/100%，spec 规则 35）。
func (s *AgentService) recordDailyUsage(before, after ai.Usage, emit func(agent.Event)) {
	delta := usage.Priced(
		after.InputTokens-before.InputTokens,
		after.OutputTokens-before.OutputTokens,
		after.CacheReadTokens-before.CacheReadTokens,
		after.CacheWriteTokens-before.CacheWriteTokens)
	if delta <= 0 {
		return
	}
	if err := s.day.Add(delta); err != nil {
		log.Printf("daily usage: %v", err)
		return
	}
	budget := s.resolve().BudgetDailyTokens
	switch s.day.CheckBudget(budget) {
	case "near":
		emit(agent.Event{Kind: agent.EvBudgetNotice,
			Text: fmt.Sprintf("今日已用 %d token（预算的 80%%+）", s.day.Total())})
	case "over":
		emit(agent.Event{Kind: agent.EvBudgetNotice,
			Text: fmt.Sprintf("今日已用 %d token，超出预算 %d", s.day.Total(), budget)})
	}
}

// DailyUsage 报告今日已计价 token 与预算（0=不限）。
func (s *AgentService) DailyUsage() (used, budget int) {
	return s.day.Total(), s.resolve().BudgetDailyTokens
}

// SessionPermissionRules lists the session's accumulated always-allow
// rules（/perms 的数据面，spec 规则 36）.
func (s *AgentService) SessionPermissionRules(sessionID string) []permission.Rule {
	return s.mgr.SessionPermissions(sessionID).SessionRules()
}

// SetFallbackModels 更新模型兜底链（spec 规则 56；空即关闭）。模型名
// 去重且首项不得与自身重复；写盘热生效（rewire 同既有路径）。
func (s *AgentService) SetFallbackModels(models []string) error {
	seen := map[string]bool{}
	var clean []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		clean = append(clean, m)
	}
	s.mu.Lock()
	s.settings.FallbackModels = clean
	view, err := s.saveSettingsLocked(s.settings)
	s.mu.Unlock()
	if err == nil {
		s.rewire()
		_ = view
	}
	return err
}

// FallbackModels 返回当前兜底链（copy）。
func (s *AgentService) FallbackModels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.settings.FallbackModels...)
}

// SetBudgetDailyTokens updates the daily budget（0=不限，spec 规则 37
// 的 config set 数据面——热生效同 SaveSettings 路径）.
func (s *AgentService) SetBudgetDailyTokens(v int) error {
	s.mu.Lock()
	cfg := s.settings
	cfg.BudgetDailyTokens = v
	_, err := s.saveSettingsLocked(cfg)
	s.mu.Unlock()
	return err
}

// SetAPIKey updates the key alone（config set apiKey 的数据面）——不
// 经 SaveSettings：它对 model 是无条件覆盖，空串会把已配置的模型抹
// 掉（config set 的组合写测试实测抓到）。
func (s *AgentService) SetAPIKey(key string) (SettingsView, error) {
	s.mu.Lock()
	cfg := s.settings
	if key != "" {
		cfg.APIKey = key
	}
	view, err := s.saveSettingsLocked(cfg)
	s.mu.Unlock()
	return view, err
}

// DailyUsageBudget reports the configured daily budget（0=不限）.
func (s *AgentService) DailyUsageBudget() int {
	return s.resolve().BudgetDailyTokens

}

// CustomCommand is one user-defined slash command（spec 规则 39）.
type CustomCommand struct {
	Name string // 文件名去扩展
	Text string // 提示词（$ARGUMENTS 待替换）
}

// customCommandsDir 是命令目录名（工作区内）.
const customCommandsDir = ".zgo/commands"

// commandCapRunes 封顶命令文件文本（rune 计，60K 对齐 stdin/粘贴/
// 资源口径——issue #195 家族清单：用户可控文本入口全部有界）。
const commandCapRunes = 60 << 10

// CustomCommands 列出工作区与全局的自定义命令（同名工作区覆盖全局，
// spec 规则 39）。SSH 工作区经 path jail 读取，同享。
func (s *AgentService) CustomCommands(workspace string) []CustomCommand {
	out := map[string]string{}
	// 全局先入，工作区覆盖（未授信的本地工作区不带工作区层——提示
	// 注入面，spec 规则 54）。
	if home, err := os.UserHomeDir(); err == nil {
		loadCommandsDir(out, filepath.Join(home, ".zgo", "commands"), nil)
	}
	if !s.workspaceTrusted(workspace) {
		return sortedCommands(out)
	}

	ws, _, err := s.openWorkspace(workspace)
	if err == nil {
		loadCommandsDir(out, customCommandsDir, &ws)
	}
	return sortedCommands(out)
}

// sortedCommands 按名排序出命令表。
func sortedCommands(out map[string]string) []CustomCommand {
	var names []string
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	cmds := make([]CustomCommand, 0, len(names))
	for _, n := range names {
		cmds = append(cmds, CustomCommand{Name: n, Text: out[n]})
	}
	return cmds
}

// loadCommandsDir 把目录下的 *.md 收进 out；ws 非空走 path jail（远
// 程同享），否则本地读。
func loadCommandsDir(out map[string]string, rel string, ws *tools.Workspace) {
	var entries []tools.DirEntry
	if ws != nil {
		entries, _ = tools.ListIn(*ws, rel)
	} else if des, err := os.ReadDir(rel); err == nil {
		for _, de := range des {
			entries = append(entries, tools.DirEntry{Name: de.Name(), IsDir: de.IsDir()})
		}
	}
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Name, ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name, ".md")
		if !validCommandName(name) {
			continue
		}
		var data []byte
		if ws != nil {
			data, _ = tools.ReadFileIn(*ws, customCommandsDir+"/"+e.Name)
		} else {
			data, _ = os.ReadFile(filepath.Join(rel, e.Name))
		}
		if len(data) == 0 {
			continue
		}
		text := string(data)
		if r := []rune(text); len(r) > commandCapRunes {
			text = string(r[:commandCapRunes]) + "…（命令文件超长，已截断）"
		}
		out[name] = text
	}
}

// validCommandName：小写字母数字与连字符（与斜杠命令形态一致）。
func validCommandName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// RedactionEnabled 报告密钥打码开关（spec 规则 73；settings 缺省 = 开）。
func (s *AgentService) RedactionEnabled() bool {
	return redact.Enabled()
}

// SetRedaction 切换工具输出的密钥打码并落盘热生效（rewire 经
// syncRedaction 同步开关与密钥集合）。
func (s *AgentService) SetRedaction(on bool) error {
	s.mu.Lock()
	s.settings.RedactSecrets = &on
	_, err := s.saveSettingsLocked(s.settings)
	s.mu.Unlock()
	if err == nil {
		s.rewire()
	}
	return err
}

// SessionDir 返回会话 JSONL 的落盘目录（诊断/测试观测面；AGENTS.md 的
// 密钥红线让「直接查转录文件」成为常规验证手段）。
func (s *AgentService) SessionDir() string {
	return filepath.Join(s.dir(), "sessions")
}

// tooldefsBaseDir 是自定义工具的工作区层目录（spec 规则 75）。
const tooldefsBaseDir = ".zgo/tools"

// tooldefRoots 取自定义工具定义根：工作区层（信任门内——工具模板
// 是注入到执行的面，未授信仓库不得自带）+ 全局层。
func (s *AgentService) tooldefRoots(sessionID, workspace string) []tooldefs.Root {
	ws := s.workspaceFor(sessionID, workspace)
	var roots []tooldefs.Root
	if ws.IsRemote() {
		roots = append(roots, tooldefs.Root{
			Base: tooldefsBaseDir, Scope: tooldefs.ScopeProject,
			FS: remoteTooldefsFS{ws.Remote},
		})
	} else if s.workspaceTrusted(workspace) {
		roots = append(roots, tooldefs.Root{
			Base:  filepath.Join(ws.Root, filepath.FromSlash(tooldefsBaseDir)),
			Scope: tooldefs.ScopeProject, FS: tooldefs.LocalFS{},
		})
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, tooldefs.Root{
			Base:  filepath.Join(home, ".zgo", "tools"),
			Scope: tooldefs.ScopeUser, FS: tooldefs.LocalFS{},
		})
	}
	return roots
}

// remoteTooldefsFS 把 RemoteFS 适配为 tooldefs.FS（与 remoteSkillsFS
// 同型）。
type remoteTooldefsFS struct{ r tools.RemoteFS }

// Join implements tooldefs.FS（根相对、斜杠形态）。
func (f remoteTooldefsFS) Join(parts ...string) string { return path.Join(parts...) }

// ReadFile implements tooldefs.FS.
func (f remoteTooldefsFS) ReadFile(name string) ([]byte, error) { return f.r.ReadFile(name) }

// ReadDir implements tooldefs.FS.
func (f remoteTooldefsFS) ReadDir(dir string) ([]tooldefs.DirEntry, error) {
	des, err := f.r.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]tooldefs.DirEntry, len(des))
	for i, de := range des {
		out[i] = tooldefs.DirEntry{Name: de.Name, IsDir: de.IsDir}
	}
	return out, nil
}

// SetAutoMemory 切换会话记忆自动化并落盘（spec 规则 74；zgo config 面）。
func (s *AgentService) SetAutoMemory(on bool) error {
	s.mu.Lock()
	s.settings.AutoMemory = &on
	_, err := s.saveSettingsLocked(s.settings)
	s.mu.Unlock()
	if err == nil {
		s.autoMemoryOn = on
	}
	return err
}

// DisableAutoMemoryForTesting 关闭收尾自动记忆（e2e 脚本 provider 按
// 回合数断言——Close 时点的额外补全会打破计数；专测自行开启）。
func (s *AgentService) DisableAutoMemoryForTesting() { s.autoMemoryOn = false }

// AutoMemoryEnabled 报告收尾自动记忆开关（spec 规则 74；缺省 = 开）。
func (s *AgentService) AutoMemoryEnabled() bool { return s.autoMemoryOn }

// ToolInfo 是 /tools 清单的一行（spec 规则 77）：名称 + 一句描述 +
// 来源（builtin/custom/mcp）。
type ToolInfo struct {
	Name        string
	Description string
	Source      string
}

// ToolsFor 列出会话的模型可见工具面（与 loop 装配同一闭包——列出的
// 就是模型看到的）。描述取首句截断。
func (s *AgentService) ToolsFor(sessionID, workspace string) []ToolInfo {
	if s.buildTools == nil {
		return nil
	}
	var out []ToolInfo
	for _, t := range s.buildTools(workspace, sessionID) {
		if t == nil {
			continue
		}
		src := "builtin"
		switch {
		case tools.IsCustomTool(t):
			src = "custom"
		case strings.HasPrefix(t.Name(), "mcp_"):
			src = "mcp"
		}
		out = append(out, ToolInfo{Name: t.Name(), Description: firstSentence(t.Description()), Source: src})
	}
	return out
}

// ToolDefDiagnostics 返回自定义工具定义的非致命问题（/tools 的 ⚠ 行）。
func (s *AgentService) ToolDefDiagnostics(sessionID, workspace string) []tooldefs.Diagnostic {
	return tooldefs.Discover(s.tooldefRoots(sessionID, workspace)).Diagnostics
}

// firstSentence 截描述首句（句号/换行前，≤80 rune）。
func firstSentence(desc string) string {
	if i := strings.IndexAny(desc, ".\n。"); i > 0 {
		desc = desc[:i]
	}
	if r := []rune(desc); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return desc
}

// SetSessionArchived 切换归档组织态（spec 规则 82）：接受 id 或唯一
// 前缀；归档不删内容——转录/搜索/导出面不变，列表与 -c 恢复默认跳过。
func (s *AgentService) SetSessionArchived(idOrPrefix string, on bool) error {
	id, ok := s.ResolveSession(idOrPrefix)
	if !ok {
		return fmt.Errorf("no unique session %q", idOrPrefix)
	}
	if !s.mgr.SetArchivedById(id, on) {
		return fmt.Errorf("archiving session: %s", id)
	}
	return nil
}

// SetHardBudgetStop 切换硬预算熔断并落盘（spec 规则 85；zgo config 面）。
func (s *AgentService) SetHardBudgetStop(on bool) error {
	s.mu.Lock()
	s.settings.HardBudgetStop = on
	_, err := s.saveSettingsLocked(s.settings)
	s.mu.Unlock()
	return err
}

// HardBudgetStopEnabled 报告硬预算熔断开关（缺省 = 关）。
func (s *AgentService) HardBudgetStopEnabled() bool { return s.resolve().HardBudgetStop }

// hardFuseErr 报告硬预算熔断态（nil = 闸开）。回合入口与子代理启动
// 共用同一判据（spec 规则 85/87）。
func (s *AgentService) hardFuseErr() error {
	cfg := s.resolve()
	if !cfg.HardBudgetStop || cfg.BudgetDailyTokens <= 0 {
		return nil
	}
	if s.day.Total() < cfg.BudgetDailyTokens {
		return nil
	}
	return fmt.Errorf("今日已计价 %d token，已达硬预算 %d——新回合被熔断（zgo config set hardBudgetStop false 可解除）",
		s.day.Total(), cfg.BudgetDailyTokens)
}

// recordSubagentUsage 把子代理运行的用量折算进当日记账（spec 规则
// 87：子代理的钱此前对记账/熔断不可见——不 emit 通知（子任务无回
// 合边界），仅入账）。
func (s *AgentService) recordSubagentUsage(u ai.Usage) {
	delta := usage.Priced(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	if delta <= 0 {
		return
	}
	if err := s.day.Add(delta); err != nil {
		log.Printf("subagent usage: %v", err)
	}
}
