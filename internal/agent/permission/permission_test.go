package permission

import "testing"

// TestCheckOrder verifies the ZCode decision order: alwaysAsk beats rules,
// session rules beat project rules, read-only defaults to allow, the rest
// asks.
func TestCheckOrder(t *testing.T) {
	svc := New(Ruleset{Rules: []Rule{
		{ID: "project.git", Tool: "bash", Args: "git *", Behavior: Allow},
	}})
	ctx := Context{Tool: "bash", Args: "git status"}

	if d := svc.Check(ctx, Capability{ReadOnly: false}); d.Behavior != Allow || d.RuleID != "project.git" {
		t.Fatalf("project rule ignored: %+v", d)
	}

	// 会话规则优先于项目规则。
	svc.GrantSession(Rule{ID: "session.s", Tool: "bash", Behavior: Deny})
	if d := svc.Check(ctx, Capability{}); d.Behavior != Deny {
		t.Fatalf("session rule ignored: %+v", d)
	}

	// alwaysAsk 压过一切规则。
	if d := svc.Check(Context{Tool: "x"}, Capability{AlwaysAsk: true}); d.Behavior != Ask || !d.Escalated {
		t.Fatalf("alwaysAsk lost: %+v", d)
	}

	// 只读工具默认放行。
	if d := svc.Check(Context{Tool: "read", Args: "a.go"}, Capability{ReadOnly: true}); d.Behavior != Allow {
		t.Fatalf("read-only asked: %+v", d)
	}

	// 无规则的普通工具询问；破坏性升级为高风险（规则压过默认升级）。
	if d := svc.Check(Context{Tool: "write"}, Capability{}); d.Behavior != Ask {
		t.Fatalf("default not ask: %+v", d)
	}
	if d := svc.Check(Context{Tool: "nuke"}, Capability{Destructive: true}); d.Behavior != Ask || d.Risk != RiskHigh {
		t.Fatalf("destructive not escalated: %+v", d)
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, subject string
		want             bool
	}{
		{"git *", "git status", true},
		{"git *", "git push origin main", true},
		{"git *", "cargo build", false},
		{"*", "anything at all", true},
		{"a.go", "a.go", true},
		{"a.go", "b.go", false},
		{"rm -rf *", "rm -rf /", true},
	}
	for _, c := range cases {
		if got := WildcardMatch(c.pattern, c.subject); got != c.want {
			t.Errorf("WildcardMatch(%q, %q) = %v", c.pattern, c.subject, got)
		}
	}
}

// TestRuleForTool verifies the "always allow" rule narrowing.
func TestRuleForTool(t *testing.T) {
	// 命令行 → 头词收窄。
	r := RuleForTool("bash", "git push origin main")
	if r.Tool != "bash" || r.Args != "git *" || r.Behavior != Allow {
		t.Fatalf("command rule = %+v", r)
	}
	// 单个参数 → 精确匹配。
	r = RuleForTool("read", "src/a.go")
	if r.Args != "src/a.go" {
		t.Fatalf("exact rule = %+v", r)
	}
	// 空参数 → 整个工具（todo 这类无参工具的合理粒度）。
	r = RuleForTool("todo", "")
	if r.Args != "" {
		t.Fatalf("bare rule = %+v", r)
	}

	// 收窄后的规则确实放行同类、拦下异类。
	svc := New(Ruleset{})
	svc.GrantSession(RuleForTool("bash", "git push origin main"))
	if d := svc.Check(Context{Tool: "bash", Args: "git status"}, Capability{}); d.Behavior != Allow {
		t.Fatalf("same head not allowed: %+v", d)
	}
	if d := svc.Check(Context{Tool: "bash", Args: "cargo build"}, Capability{}); d.Behavior != Ask {
		t.Fatalf("other command allowed: %+v", d)
	}
}

// TestPlanModeDeniesSideEffects verifies plan mode beats session rules and
// keeps read-only tools running (ZCode's plan-mode-policy).
func TestPlanModeDeniesSideEffects(t *testing.T) {
	svc := New(Ruleset{})
	svc.GrantSession(Rule{ID: "s", Tool: "write", Behavior: Allow})
	svc.SetMode(ModePlan)

	if d := svc.Check(Context{Tool: "write"}, Capability{}); d.Behavior != Deny {
		t.Fatalf("plan mode allowed write: %+v", d)
	}
	if d := svc.Check(Context{Tool: "read"}, Capability{ReadOnly: true}); d.Behavior != Allow {
		t.Fatalf("plan mode blocked read: %+v", d)
	}
	// readonly bash 判定在计划模式下同样可用。
	if d := svc.Check(Context{Tool: "bash", Args: "command: git status"},
		Capability{ReadOnly: true}); d.Behavior != Allow {
		t.Fatalf("plan mode blocked readonly bash: %+v", d)
	}

	svc.SetMode(ModeNormal)
	if d := svc.Check(Context{Tool: "write"}, Capability{}); d.Behavior != Allow {
		t.Fatalf("session rule not restored: %+v", d)
	}
}
