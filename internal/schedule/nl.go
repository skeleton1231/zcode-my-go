package schedule

// 自然语言 cron（spec 规则 95）：把常见的中文调度口语翻成五字段
// 表达式（cron.go 的 ParseCron 仍是唯一权威——这里只做翻译，产出必
// 须经它校验）。语料有界：频率（每天/工作日/周末/每周X/每N分钟/每N
// 小时）× 时刻（HH:MM / 时段词+数字 / 午夜/中午）。解析不了的明确
// 报错并建议 cron 原文——歧义不静默兜底。

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParseNatural 把一句中文调度口语翻成五字段 cron 表达式。
// 不认识的输入返回错误（附建议），绝不猜。
func ParseNatural(s string) (string, error) {
	src := strings.TrimSpace(s)
	if src == "" {
		return "", fmt.Errorf("空的自然语言调度")
	}
	t := normalizeNL(src)
	minute, hour, hasTime := -1, -1, false

	// 时刻先行抽出（任何频率都可带）。
	if h, m, ok := extractClock(t); ok {
		hour, minute, hasTime = h, m, true
	} else if h, m, ok := extractHourWord(t); ok {
		hour, minute, hasTime = h, m, true
	}

	switch {
	case strings.HasPrefix(t, "每") && strings.Contains(t, "分钟"):
		n := stepOf(t)
		if n <= 0 || n > 59 {
			return "", fmt.Errorf("每 N 分钟的 N 需在 1..59（%q）", src)
		}
		return fmt.Sprintf("*/%d * * * *", n), nil
	case strings.HasPrefix(t, "每") && strings.Contains(t, "小时"):
		n := stepOf(t)
		if n <= 0 || n > 23 {
			return "", fmt.Errorf("每 N 小时的 N 需在 1..23（%q）", src)
		}
		// 「每小时第30分」：整点外的分钟口语。
		if !hasTime {
			if m := minuteWord(t); m >= 0 {
				minute = m
			}
		}
		if n == 1 {
			return fmt.Sprintf("%d * * * *", minuteOfHour(minute)), nil
		}
		return fmt.Sprintf("%d */%d * * *", minuteOfHour(minute), n), nil
	case hasPrefix(t, "每天", "每日", "每日间", "每天间"):
		if !hasTime {
			return "", fmt.Errorf("%q 缺少时刻（如：每天下午3点 / 每天 15:00）", src)
		}
		return fmt.Sprintf("%d %d * * *", minute, hour), nil
	case hasPrefix(t, "工作日", "每工作日", "平日", "工作天的", "工作日间"):
		if !hasTime {
			return "", fmt.Errorf("%q 缺少时刻（如：工作日早上9点）", src)
		}
		return fmt.Sprintf("%d %d * * 1-5", minute, hour), nil
	case hasPrefix(t, "周末", "每周末"):
		if !hasTime {
			return "", fmt.Errorf("%q 缺少时刻（如：周末上午10点）", src)
		}
		return fmt.Sprintf("%d %d * * 0,6", minute, hour), nil
	case containsAny(t, weekdays...): // 每周X/每星期X/每礼拜X（含裸 周一9点）
		dow, rest, ok := extractWeekday(t)
		if !ok {
			return "", fmt.Errorf("%q 认不出星期几（周一..周日）", src)
		}
		_ = rest
		if !hasTime {
			return "", fmt.Errorf("%q 缺少时刻（如：每周一 8:30）", src)
		}
		return fmt.Sprintf("%d %d * * %d", minute, hour, dow), nil
	case strings.HasPrefix(t, "每月") && hasTime:
		dom, rest, ok := extractDayOfMonth(t)
		if !ok {
			return "", fmt.Errorf("%q 认不出几号（如：每月1号 9:00）", src)
		}
		_ = rest
		return fmt.Sprintf("%d %d %d * *", minute, hour, dom), nil
	}
	return "", fmt.Errorf("认不出 %q——支持的口语见 zgo cron add 帮助；复杂调度请直接给五字段 cron 表达式（--schedule）", src)
}

// normalizeNL 压空白、去顿号/冒号变体外的全角符号（：→:，０-９→0-9）。
func normalizeNL(s string) string {
	s = strings.Join(strings.Fields(s), "")
	s = strings.NewReplacer("：", ":", "，", ",", "。", "").Replace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= '０' && r <= '９' {
			b.WriteRune(r - '０' + '0')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var clockRe = regexp.MustCompile(`(\d{1,2}):(\d{2})`)

// extractClock 抽 HH:MM（优先级最高的时刻形态）。
func extractClock(t string) (hour, minute int, ok bool) {
	m := clockRe.FindStringSubmatch(t)
	if m == nil {
		return 0, 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if h > 23 || mi > 59 {
		return 0, 0, false
	}
	return h, mi, true
}

// 时段词 → 小时偏移。
var periodWords = []struct {
	word  string
	delta int
}{
	{"凌晨", 0}, {"早上", 0}, {"早晨", 0}, {"上午", 0},
	{"中午", 12}, {"午后", 12}, {"下午", 12}, {"傍晚", 12},
	{"晚上", 12}, {"今晚", 12},
}

var hourWordRe = regexp.MustCompile(`(\d{1,2})[点时]`)

// extractHourWord 抽「时段词+N点」——晚上8点=20、下午3点=15、中午12点=12。
// 无时段词的裸 N 点按 24 小时制（8点=8）。
func extractHourWord(t string) (hour, minute int, ok bool) {
	m := hourWordRe.FindStringSubmatch(t)
	if m == nil {
		return 0, 0, false
	}
	h, _ := strconv.Atoi(m[1])
	// 「N点半/三十」是常见口语。
	minute = 0
	if strings.Contains(t, "半") {
		minute = 30
	} else if strings.Contains(t, "三十") || strings.Contains(t, "30") {
		minute = 30
	}
	for _, p := range periodWords {
		if strings.Contains(t, p.word) {
			h += p.delta
			// 口语的 12 点跨段修正：中午12点=12（不是24）、晚上12点=0
			//（午夜）；下午12点口语即正午=12。
			if h == 24 {
				if p.word == "晚上" || p.word == "今晚" {
					h = 0
				} else {
					h = 12
				}
			}
			break
		}
	}
	if h > 23 {
		return 0, 0, false
	}
	return h, minute, true
}

// stepOf 抽「每N」的 N（缺省 1）。
func stepOf(t string) int {
	m := regexp.MustCompile(`每(\d*)`).FindStringSubmatch(t)
	if m == nil || m[1] == "" {
		return 1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

func minuteOfHour(minute int) int {
	if minute < 0 {
		return 0
	}
	return minute
}

var minuteWordRe = regexp.MustCompile(`第?(\d{1,2})分`)

// minuteWord 抽「第30分 / 30分」的分钟；无匹配返回 -1。
func minuteWord(t string) int {
	m := minuteWordRe.FindStringSubmatch(t)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	if n > 59 {
		return -1
	}
	return n
}

var weekdays = []string{"周一", "星期一", "礼拜一", "周二", "星期二", "礼拜二",
	"周三", "星期三", "礼拜三", "周四", "星期四", "礼拜四",
	"周五", "星期五", "礼拜五", "周六", "星期六", "礼拜六",
	"周日", "星期日", "礼拜日", "周天", "星期天", "礼拜天"}

// extractWeekday 抽星期几（周一=1..周五=5，周六/周日=6/0）。
func extractWeekday(t string) (dow int, rest string, ok bool) {
	for i, w := range weekdays {
		if strings.Contains(t, w) {
			// 列表按「周一/星期一/礼拜一」三元组排列：组号 i/3 即
			// 周一..周六（0..5 → 1..6），第 7 组（i>=18）是周日 → 0。
			d := i/3 + 1
			if i >= 18 {
				d = 0
			}
			return d, strings.Replace(t, w, "", 1), true
		}
	}
	return 0, "", false
}

var domRe = regexp.MustCompile(`(\d{1,2})[号日]`)

// extractDayOfMonth 抽「N号/N日」。
func extractDayOfMonth(t string) (dom int, rest string, ok bool) {
	m := domRe.FindStringSubmatch(t)
	if m == nil {
		return 0, "", false
	}
	d, _ := strconv.Atoi(m[1])
	if d < 1 || d > 31 {
		return 0, "", false
	}
	return d, strings.Replace(t, m[0], "", 1), true
}

func hasPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
