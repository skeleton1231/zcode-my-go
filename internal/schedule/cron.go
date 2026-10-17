// Package schedule is Zgo's automation core (ZCode's automation
// experience): five-field cron expressions, a hot-reloaded job file, and a
// minute-tick scheduler that runs agent prompts unattended.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Field bounds: minute hour day-of-month month day-of-week.
var fieldBounds = [5][2]int{
	{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7},
}

// Schedule is a parsed five-field cron expression: each field is a set of
// allowed values (weekday 7 folds onto 0 — both are Sunday).
type Schedule struct {
	fields [5]map[int]bool
}

// ParseCron parses a five-field expression（分 时 日 月 周，本地时区）：
// each field takes "*", "*/n", "n", "a-b" and comma lists thereof.
func ParseCron(expr string) (Schedule, error) {
	var s Schedule
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return s, fmt.Errorf("cron 表达式需要 5 个字段（分 时 日 月 周），得到 %d 个: %q", len(parts), expr)
	}
	for i, part := range parts {
		set, err := parseField(part, fieldBounds[i][0], fieldBounds[i][1])
		if err != nil {
			return s, fmt.Errorf("第 %d 个字段 %q: %w", i+1, part, err)
		}
		if i == 4 && set[7] { // 周字段的 7 折叠为周日
			set[0] = true
			delete(set, 7)
		}
		s.fields[i] = set
	}
	return s, nil
}

// parseField expands one field into its value set.
func parseField(part string, min, max int) (map[int]bool, error) {
	set := map[int]bool{}
	for _, token := range strings.Split(part, ",") {
		if token == "" {
			return nil, fmt.Errorf("空 token")
		}
		step := 1
		body := token
		if i := strings.Index(token, "/"); i >= 0 {
			body = token[:i]
			// strconv 全串校验（issue #113）：Sscanf 的 lenient 语义忽略
			// 错误——*/x 与 */ 曾静默落成每分钟触发，无人值守 agent 按分
			// 钟烧 token。Atoi 拒绝一切非纯数字。
			var err error
			if step, err = strconv.Atoi(token[i+1:]); err != nil || step < 1 {
				return nil, fmt.Errorf("步长 %q 无效", token[i+1:])
			}
		}
		lo, hi := min, max
		switch {
		case body == "*":
			// 全域（可带步长）
		case strings.Count(body, "-") >= 1:
			// 区间只允许一个 -，两段各自 Atoi 全串校验（1-2-3 曾被吞成 1-2）。
			ends := strings.SplitN(body, "-", 2)
			if strings.Contains(ends[1], "-") {
				return nil, fmt.Errorf("区间 %q 无效", body)
			}
			var err error
			if lo, err = strconv.Atoi(ends[0]); err != nil {
				return nil, fmt.Errorf("区间 %q 无效", body)
			}
			if hi, err = strconv.Atoi(ends[1]); err != nil || lo > hi {
				return nil, fmt.Errorf("区间 %q 无效", body)
			}
		default:
			var err error
			if lo, err = strconv.Atoi(body); err != nil {
				return nil, fmt.Errorf("数值 %q 无效", body)
			}
			hi = lo
			if step != 1 {
				return nil, fmt.Errorf("单个数值 %q 不能带步长", body)
			}
		}
		if lo < min || hi > max {
			return nil, fmt.Errorf("越界: 允许 %d-%d", min, max)
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("空字段")
	}
	return set, nil
}

// Matches reports whether t hits the schedule（分钟粒度）.
func (s Schedule) Matches(t time.Time) bool {
	return s.fields[0][t.Minute()] &&
		s.fields[1][t.Hour()] &&
		s.fields[2][t.Day()] &&
		s.fields[3][int(t.Month())] &&
		s.fields[4][int(t.Weekday())]
}

// Next returns the next matching minute strictly after `after`（本地时区，
// 向前扫描上限 4 年，超出即零值——表达式恒不匹配时不出死循环）.
func (s Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(4, 0, 0)
	for t.Before(limit) {
		if s.Matches(t) {
			return t
		}
		// 日级不匹配时直接跳到次日零点（月的部分随之快进），否则按分钟走。
		if !s.fields[2][t.Day()] || !s.fields[3][int(t.Month())] ||
			(!s.fields[4][int(t.Weekday())] && t.Hour() == 0 && t.Minute() == 0) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).
				AddDate(0, 0, 1)
			continue
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
