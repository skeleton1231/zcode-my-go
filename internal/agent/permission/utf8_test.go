package permission

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRuleForToolLongChineseRoundTrip 验证长中文参数生成的规则是合法
// UTF-8 且能匹配原始参数（issue #12：字节截断曾产生非法 UTF-8，下一次
// 权限检查在 regexp.MustCompile 上 panic）。
func TestRuleForToolLongChineseRoundTrip(t *testing.T) {
	longPath := strings.Repeat("项", 50) // 150 字节，超过 40 rune 上限
	rule := RuleForTool("edit", longPath)
	if !utf8.ValidString(rule.ID) || !utf8.ValidString(rule.Args) {
		t.Fatalf("rule contains invalid UTF-8: ID=%q Args=%q", rule.ID, rule.Args)
	}
	if !WildcardMatch(rule.Args, longPath) {
		t.Fatalf("rule %q does not match its origin arg", rule.Args)
	}
	// 别的长参数不得被误放行。
	if WildcardMatch(rule.Args, strings.Repeat("别的", 50)) {
		t.Fatal("rule overmatches unrelated args")
	}
}

// TestRuleForToolLongCommand 验证命令行走头词收窄且匹配完整命令。
func TestRuleForToolLongCommand(t *testing.T) {
	cmd := "git push " + strings.Repeat("远", 60)
	rule := RuleForTool("bash", cmd)
	if rule.Args != "git *" {
		t.Fatalf("args = %q, want git *", rule.Args)
	}
	if !WildcardMatch(rule.Args, cmd) {
		t.Fatal("git * does not match the origin command")
	}
}

// TestRuleForToolShortExact 验证短参数仍为精确匹配（不因修复放宽）。
func TestRuleForToolShortExact(t *testing.T) {
	rule := RuleForTool("edit", "a.go")
	if rule.Args != "a.go" {
		t.Fatalf("args = %q, want exact", rule.Args)
	}
	if WildcardMatch(rule.Args, "a.go.bak") {
		t.Fatal("exact rule overmatches")
	}
}

// TestWildcardMatchToleratesInvalidPattern 验证非法 pattern（如历史损坏
// 数据中的非法 UTF-8）返回 false 而非 panic。
func TestWildcardMatchToleratesInvalidPattern(t *testing.T) {
	if WildcardMatch("bad\xe9pattern*", "anything") {
		t.Fatal("invalid pattern should not match")
	}
}
