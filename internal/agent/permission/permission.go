// Package permission is the tool permission system, translated from
// ZCode's core/src/permission (service.ts, rule-matching.ts): behaviors
// allow/ask/deny, session-scoped and project rule sets, and per-tool
// capability declarations that drive the default decision.
package permission

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
)

// Behavior is a permission decision.
type Behavior string

// Behaviors.
const (
	Allow Behavior = "allow"
	Ask   Behavior = "ask"
	Deny  Behavior = "deny"
)

// Mode is the collaboration mode (ZCode's CollaborationMode): plan mode
// denies every side effect, keeping the agent to reading and proposing.
type Mode string

// Collaboration modes.
const (
	ModeNormal Mode = "normal"
	ModePlan   Mode = "plan"
)

// RiskLevel of a tool call, from ZCode's RiskLevel.
type RiskLevel string

// Risk levels.
const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// Capability is what a tool declares about itself, from ZCode's
// PermissionToolCapability: read-only tools run without asking, alwaysAsk
// tools confirm every call regardless of rules, destructive ones escalate.
type Capability struct {
	ReadOnly    bool
	AlwaysAsk   bool
	Destructive bool
	Risk        RiskLevel
}

// Rule grants a behavior to a tool, optionally narrowed by a wildcard
// pattern over the call's canonical argument line (ZCode's
// PermissionRuleValue subjects, e.g. "bash: git *").
type Rule struct {
	ID       string
	Tool     string // "*" or a tool name
	Args     string // optional wildcard pattern, e.g. "git *"
	Behavior Behavior
}

// Ruleset is an ordered set of rules; first match wins.
type Ruleset struct {
	Rules []Rule
}

// Context describes the call being checked.
type Context struct {
	Tool string
	Args string // canonical one-line argument summary for pattern matching
}

// Decision is the outcome of a check, from ZCode's PermissionDecisionResult.
type Decision struct {
	Behavior  Behavior
	RuleID    string
	Reason    string
	Escalated bool // the decision came from capability escalation, not a rule
	Risk      RiskLevel
}

// Service holds the session-scoped rules ("always allow in this session",
// dying with the session as ZCode's per-app instance does) over project
// rules, the collaboration mode, and decides calls.
type Service struct {
	mu      sync.Mutex
	session Ruleset
	project Ruleset
	mode    Mode
}

// New builds a service over immutable project rules.
func New(project Ruleset) *Service {
	return &Service{project: project}
}

// SetMode switches the collaboration mode (ZCode's plan-mode transitions).
func (s *Service) SetMode(mode Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

// Mode returns the current collaboration mode.
func (s *Service) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// GrantSession adds a session-scoped rule (ZCode's grantSessionPermission).
func (s *Service) GrantSession(rule Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session.Rules = append(s.session.Rules, rule)
}

// SetProjectRules 替换项目规则集（工作区授信后重载，spec 规则 54）。
func (s *Service) SetProjectRules(project Ruleset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.project = project
}

// SessionRules returns the session rules for display.
func (s *Service) SessionRules() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Rule(nil), s.session.Rules...)
}

// Check decides a call. The order follows ZCode's resolveCapability flow:
// alwaysAsk beats everything; rules beat defaults; read-only defaults to
// allow; everything else asks.
func (s *Service) Check(ctx Context, cap Capability) Decision {
	risk := cap.Risk
	if risk == "" {
		risk = RiskMedium
	}
	if cap.AlwaysAsk {
		return Decision{Behavior: Ask, Risk: risk,
			Reason: "tool declares alwaysAsk", Escalated: true}
	}
	s.mu.Lock()
	session, project, mode := s.session, s.project, s.mode
	s.mu.Unlock()
	// Plan 模式先于一切规则拒绝副作用（ZCode plan-mode-policy）：计划
	// 阶段只读，会话规则也不能越过它。
	if mode == ModePlan && !cap.ReadOnly {
		return Decision{Behavior: Deny, Risk: risk,
			Reason: "plan mode: 只读工具可用，改动类工具被拒绝；请先给出计划", Escalated: true}
	}

	if d, ok := match(session, ctx); ok {
		return d
	}
	if d, ok := match(project, ctx); ok {
		return d
	}
	if cap.ReadOnly {
		return Decision{Behavior: Allow, Risk: risk,
			Reason: "read-only tool", Escalated: true}
	}
	// ZCode 默认语义：普通工具在无规则时询问；破坏性工具同样询问并标注。
	if cap.Destructive {
		return Decision{Behavior: Ask, Risk: RiskHigh,
			Reason: "destructive tool", Escalated: true}
	}
	return Decision{Behavior: Ask, Risk: risk,
		Reason: "no matching rule", Escalated: true}
}

// match finds the first rule that fits the call. Argument patterns match
// the call's canonical line ("command: git status"); a pattern without a
// key prefix also matches the value alone, so "git *" works as written.
func match(set Ruleset, ctx Context) (Decision, bool) {
	for _, r := range set.Rules {
		if r.Tool != "*" && r.Tool != ctx.Tool {
			continue
		}
		if r.Args != "" && !WildcardMatch(r.Args, ctx.Args) &&
			!WildcardMatch(r.Args, stripKeyPrefix(ctx.Args)) {
			continue
		}
		return Decision{Behavior: r.Behavior, RuleID: r.ID, Risk: RiskMedium,
			Reason: "rule " + r.ID}, true
	}
	return Decision{}, false
}

// stripKeyPrefix removes a leading "key: " from a canonical argument line
// ("command: git status" → "git status"); lines without one pass through.
func stripKeyPrefix(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ' ' && i > 0 {
				return s[i+2:]
			}
			return s
		}
		if !(s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z' ||
			s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
			return s // 非 word: 前缀，原样返回
		}
	}
	return s
}

// WildcardMatch is ZCode's wildcardToRegExp: * spans anything, the rest is
// literal. Rules are persisted data and may be damaged (illegal UTF-8 and
// such): a pattern that fails to compile matches nothing — never a panic
// (issue #12).
func WildcardMatch(pattern, subject string) bool {
	expr := "^" + wildcardExpr(pattern) + "$"
	if v, ok := patternCache.Load(expr); ok {
		return v.(*regexp.Regexp).MatchString(subject)
	}
	re, err := regexp.Compile(expr) // 编译失败：视作不匹配，不 panic
	if err != nil {
		return false
	}
	patternCache.Store(expr, re)
	return re.MatchString(subject)
}

// patternCache 缓存已编译 pattern：Check 在每次工具调用路径上，避免
// 重复编译（issue #12 建议）。
var patternCache sync.Map // string → *regexp.Regexp

// wildcardExpr 把 * 通配模式编译为正则字面量。
func wildcardExpr(pattern string) string {
	parts := split(pattern, '*')
	var expr string
	for i, p := range parts {
		if i > 0 {
			expr += ".*"
		}
		expr += regexp.QuoteMeta(p)
	}
	return expr
}

func split(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// RuleForTool builds the rule an "always allow" answer stores, mirroring
// ZCode's session rule subjects: a bare argument line narrows to an exact
// match, a command line narrows to its head word ("git push origin" →
// "git *"), and nothing fits the whole tool forever.
// maxRuleSubjectRunes 是规则 subject 的截断上限，按 rune 计：按字节截
// 断中文会在字符中间切开，产生非法 UTF-8 的规则 ID/Args（issue #12）。
const maxRuleSubjectRunes = 40

func RuleForTool(tool, args string) Rule {
	subject := args
	truncated := false
	if runes := []rune(subject); len(runes) > maxRuleSubjectRunes {
		subject = string(runes[:maxRuleSubjectRunes])
		truncated = true
	}
	rule := Rule{ID: "session." + tool, Tool: tool, Behavior: Allow}
	if subject == "" {
		return rule
	}
	rule.ID += "." + subject
	if head, rest, found := cut(subject, ' '); found && rest != "" {
		rule.Args = head + " *"
	} else if truncated {
		// 截断后的单 token 精确匹配不了原参数：退化为前缀匹配，
		// 仍窄于整工具放行（issue #12）。
		rule.Args = subject + "*"
	} else {
		rule.Args = subject // 精确匹配（单文件、单命令）
	}
	return rule
}

func cut(s string, sep byte) (head, rest string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// ParseRuleset decodes project rules (the workspace's .zgorules.json):
// strict validation — a malformed file is an error, never a silent ignore.
func ParseRuleset(data []byte) (Ruleset, error) {
	var file struct {
		Version int    `json:"version"`
		Rules   []Rule `json:"rules"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return Ruleset{}, fmt.Errorf("parsing rules: %w", err)
	}
	if file.Version != 1 {
		return Ruleset{}, fmt.Errorf("unsupported rules version %d (want 1)", file.Version)
	}
	for i, r := range file.Rules {
		if r.Tool == "" {
			return Ruleset{}, fmt.Errorf("rule %d: empty tool", i+1)
		}
		switch r.Behavior {
		case Allow, Ask, Deny:
		default:
			return Ruleset{}, fmt.Errorf("rule %d: behavior must be allow/ask/deny, got %q", i+1, r.Behavior)
		}
	}
	return Ruleset{Rules: file.Rules}, nil
}
