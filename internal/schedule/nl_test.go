package schedule

// 自然语言 cron 测试（spec 规则 95）：语料矩阵 + 错误面 + 产出往返
// ParseCron 校验。

import (
	"testing"
)

func TestParseNaturalTable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"每天 15:00", "0 15 * * *"},
		{"每天下午3点", "0 15 * * *"},
		{"每天下午3点半", "30 15 * * *"},
		{"每日早上8:30", "30 8 * * *"},
		{"每天晚上11点", "0 23 * * *"},
		{"每天凌晨2点", "0 2 * * *"},
		{"每天中午12点", "0 12 * * *"},
		{"每天晚上12点", "0 0 * * *"},
		{"每天下午12点", "0 12 * * *"},
		{"工作日早上9点", "0 9 * * 1-5"},
		{"每工作日 9:00", "0 9 * * 1-5"},
		{"周末上午10点", "0 10 * * 0,6"},
		{"每周一 8:30", "30 8 * * 1"},
		{"每周三下午3点", "0 15 * * 3"},
		{"每周日晚上8点", "0 20 * * 0"},
		{"每星期五17:00", "0 17 * * 5"},
		{"每月1号 9:00", "0 9 1 * *"},
		{"每月15日10:30", "30 10 15 * *"},
		{"每小时", "0 * * * *"},
		{"每小时第30分", "30 * * * *"},
		{"每30分钟", "*/30 * * * *"},
		{"每15分钟", "*/15 * * * *"},
		{"每2小时", "0 */2 * * *"},
		{"每6小时30分", "30 */6 * * *"},
		// 全角与空白归一。
		{"每天 １５：００", "0 15 * * *"},
	}
	for _, c := range cases {
		got, err := ParseNatural(c.in)
		if err != nil {
			t.Fatalf("ParseNatural(%q) = %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseNatural(%q) = %q, want %q", c.in, got, c.want)
		}
		// 往返：产出必须被权威解析器接受。
		if _, err := ParseCron(got); err != nil {
			t.Fatalf("round trip %q -> %q: %v", c.in, got, err)
		}
	}
}

func TestParseNaturalErrors(t *testing.T) {
	for _, bad := range []string{
		"", "每天中午", "随便一句话", "每天", "每周一", "工作日", "每周八下午3点",
		"每0分钟", "每99分钟", "每99小时", "每月 9:00", "每月40号 9:00",
		"每天25点", "每天24:00", "run at 3pm",
	} {
		if got, err := ParseNatural(bad); err == nil {
			t.Fatalf("ParseNatural(%q) = %q, want error", bad, got)
		}
	}
}
