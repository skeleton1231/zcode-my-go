package redact

// 打码器单元：模式族命中/不误报、精确密钥、短串跳过、开关、幂等、
// Bytes 零拷贝路径。夹具一律运行时拼接的低熵重复串——形状匹配不看
// 熵，源码里既不落完整密钥形态也不落高熵随机串（源码扫描器面前
// 的自我修养）。

import (
	"strings"
	"testing"
)

// concat/rep 是夹具构造帮手。
func concat(parts ...string) string { return strings.Join(parts, "") }

func rep(ch string, n int) string { return strings.Repeat(ch, n) }

func TestPatternFamilies(t *testing.T) {
	cases := []struct{ in, wantTag string }{
		{concat("key=", "sk-ant-", "api03-", rep("a", 24)), "anthropic-key"},
		{concat("export OPENAI_API_KEY=", "sk-", rep("b", 24)), "openai-key"},
		{concat("token: ", "gh", "p_", rep("c", 31), "AB"), "github-token"},
		{concat("", "github_", "pat_", rep("d", 31)), "github-token"},
		{concat("aws ", "AKIA", "0123456789ABCDEF", " found"), "aws-key"},
		{concat("g=", "AIza", "0123456789", rep("g", 25)), "google-key"},
		{concat("auth0 ", "eyJ", rep("e", 12), ".", rep("f", 12), ".", rep("9", 12)), "jwt"},
	}
	for _, c := range cases {
		out := String(c.in)
		if !strings.Contains(out, "[REDACTED:"+c.wantTag+"]") {
			t.Fatalf("family %s: %q -> %q", c.wantTag, c.in, out)
		}
	}
}

func TestNoFalsePositives(t *testing.T) {
	benign := []string{
		"task: fix the sk-learn import",        // sk-learn 短于 20
		"AKIA is an AWS prefix people mention", // 长度不足
		"AIza-short",                           // 不足 35
		"AIza plus 分隔的说明文字 ok",                 // 前缀后接非键字符
		"normal text with numbers 1234567890 and words",
		"func main() { println(\"eyJ\") }", // 单 eyJ 无三段
		"", "   ",
	}
	for _, s := range benign {
		if out := String(s); out != s {
			t.Fatalf("false positive: %q -> %q", s, out)
		}
	}
}

func TestExactSecrets(t *testing.T) {
	t.Cleanup(func() { Set() }) // 清场：别的测试不受影响
	// 无任何模式前缀——只有精确匹配能抓。
	secret := concat("a1b2c3d4e5f6.", "glmkey9876543210")
	Set(secret)
	out := String("GLM_KEY=" + secret + " 其余内容")
	if !strings.Contains(out, "[REDACTED:secret]") || strings.Contains(out, secret) {
		t.Fatalf("exact: %q", out)
	}
	// 长者优先：同前缀的两个 key，先换长的。
	short := secret + "extra"
	Set(short, secret)
	out = String("value=" + short)
	if strings.Contains(out, secret) { // 短的残留 = 顺序错了
		t.Fatalf("longest-first violated: %q", out)
	}
}

func TestShortSecretsSkipped(t *testing.T) {
	t.Cleanup(func() { Set() })
	Set("short", "1234567")
	if out := String("this short text stays intact"); out != "this short text stays intact" {
		t.Fatalf("short secret mangled text: %q", out)
	}
}

func TestEnabledToggle(t *testing.T) {
	t.Cleanup(func() { SetEnabled(true); Set() })
	SetEnabled(false)
	if out := String(concat("k=", "sk-ant-", rep("a", 20))); strings.Contains(out, "REDACTED") {
		t.Fatalf("disabled but redacted: %q", out)
	}
	SetEnabled(true)
	if out := String(concat("k=", "sk-ant-", rep("a", 20))); !strings.Contains(out, "REDACTED") {
		t.Fatalf("enabled but not redacted: %q", out)
	}
}

func TestIdempotent(t *testing.T) {
	t.Cleanup(func() { Set() })
	once := String(concat("k=", "sk-ant-", rep("a", 20), " more"))
	twice := String(once)
	if once != twice {
		t.Fatalf("not idempotent: %q vs %q", once, twice)
	}
}

func TestBytesZeroCopyNoHit(t *testing.T) {
	t.Cleanup(func() { Set() })
	buf := []byte("completely ordinary output")
	out := Bytes(buf)
	if &out[0] != &buf[0] {
		t.Fatal("no-hit path should return the same slice")
	}
}

func TestBytesExactNoTriggerChars(t *testing.T) {
	t.Cleanup(func() { Set() })
	// 低熵（高熵随机串会被源码扫描器误报；打码器只看长度与精确相
	// 等，熵无关）。不含任何模式触发子串。
	secret := strings.Repeat("ab12", 6)
	Set(secret)
	out := Bytes([]byte("ZGO_API_KEY=" + secret))
	if strings.Contains(string(out), secret) {
		t.Fatalf("exact secret without trigger chars survived: %q", out)
	}
}
