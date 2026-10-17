// Package permission is the tool permission system, translated from
// ZCode's core/src/permission (service.ts, rule-matching.ts): behaviors
// allow/ask/deny, session-scoped and project rule sets, and per-tool
// capability declarations that drive the default decision.
package permission

import (
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
// rules, and decides calls.
type Service struct {
	mu      sync.Mutex
	session Ruleset
	project Ruleset
}

// New builds a service over immutable project rules.
func New(project Ruleset) *Service {
	return &Service{project: project}
}

// GrantSession adds a session-scoped rule (ZCode's grantSessionPermission).
func (s *Service) GrantSession(rule Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session.Rules = append(s.session.Rules, rule)
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
	session, project := s.session, s.project
	s.mu.Unlock()

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

// match finds the first rule that fits the call.
func match(set Ruleset, ctx Context) (Decision, bool) {
	for _, r := range set.Rules {
		if r.Tool != "*" && r.Tool != ctx.Tool {
			continue
		}
		if r.Args != "" && !WildcardMatch(r.Args, ctx.Args) {
			continue
		}
		return Decision{Behavior: r.Behavior, RuleID: r.ID, Risk: RiskMedium,
			Reason: "rule " + r.ID}, true
	}
	return Decision{}, false
}

// WildcardMatch is ZCode's wildcardToRegExp: * spans anything, the rest is
// literal.
func WildcardMatch(pattern, subject string) bool {
	parts := split(pattern, '*')
	var expr string
	for i, p := range parts {
		if i > 0 {
			expr += ".*"
		}
		expr += regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + expr + "$").MatchString(subject)
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
func RuleForTool(tool, args string) Rule {
	subject := args
	if len(subject) > 40 {
		subject = subject[:40]
	}
	rule := Rule{ID: "session." + tool, Tool: tool, Behavior: Allow}
	if subject == "" {
		return rule
	}
	rule.ID += "." + subject
	if head, rest, found := cut(subject, ' '); found && rest != "" {
		rule.Args = head + " *"
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
