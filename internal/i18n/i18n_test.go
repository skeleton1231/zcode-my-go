package i18n

// 语言检测与目录测试（spec 规则 94）：归一矩阵、auto 解析、未知回
// 退、双语目录非空。

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]Locale{
		"zh":             Zh,
		"zh_CN":          Zh,
		"zh-CN.UTF-8":    Zh,
		"en_US":          En,
		"en":             En,
		"EN_US@pinyin":   En,
		"C":              "",
		"POSIX":          "",
		"ja_JP":          "",
		"":               "",
		"garbage-locale": "",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Fatalf("normalize(%q) = %q want %q", in, got, want)
		}
	}
}

func TestDetectEnvOrder(t *testing.T) {
	t.Setenv("LANG", "zh_CN.UTF-8")
	if got := DetectEnv(); got != Zh {
		t.Fatalf("LANG only = %q", got)
	}
	t.Setenv("LC_MESSAGES", "en_US.UTF-8")
	if got := DetectEnv(); got != En {
		t.Fatalf("LC_MESSAGES beats LANG = %q", got)
	}
	t.Setenv("ZGO_LANG", "zh")
	if got := DetectEnv(); got != Zh {
		t.Fatalf("ZGO_LANG wins = %q", got)
	}
	t.Setenv("ZGO_LANG", "C")
	if got := DetectEnv(); got != En {
		t.Fatalf("unrecognized ZGO_LANG falls through = %q", got)
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("LANG", "en_US.UTF-8")
	if got := Resolve(""); got != En {
		t.Fatalf("auto(env) = %q", got)
	}
	if got := Resolve("auto"); got != En {
		t.Fatalf("auto = %q", got)
	}
	if got := Resolve("zh"); got != Zh {
		t.Fatalf("explicit zh = %q", got)
	}
	if got := Resolve("bogus"); got != En {
		t.Fatalf("bogus falls to env then default = %q", got)
	}
	t.Setenv("LANG", "C")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("ZGO_LANG", "")
	if got := Resolve(""); got != Zh {
		t.Fatalf("no signal = %q (zh is the baseline)", got)
	}
}

// TestCatalogsFilled：双语目录的关键面都非空且语言标记正确——
// struct 同构让「缺键」编译期挡住，这里挡「改漏」（空串）。
func TestCatalogsFilled(t *testing.T) {
	for _, l := range []Locale{Zh, En} {
		c := Catalog(l)
		if c.Lang != l {
			t.Fatalf("lang tag = %q", c.Lang)
		}
		for _, s := range []string{
			c.CLI.Help, c.CLI.NeedPrompt, c.CLI.UnknownFlag, c.CLI.UnknownKey,
			c.CLI.BadLangValue, c.CLI.NoSessions, c.CLI.UsageDaysBrief,
			c.TUI.Idle, c.TUI.Running, c.TUI.Stopping, c.TUI.Rerunning, c.TUI.Aborted,
			c.Budget.Near, c.Budget.Over, c.Budget.Fuse,
		} {
			if strings.TrimSpace(s) == "" {
				t.Fatalf("locale %q has an empty string", l)
			}
		}
	}
	// en 确实换语（不是 zh 的复制）。
	if Catalog(En).TUI.Running == Catalog(Zh).TUI.Running {
		t.Fatal("en catalog equals zh")
	}
}
