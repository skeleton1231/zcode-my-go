package schedule

// 时间窗（spec 规则 64，pi off-peak 的 Zgo 形态）：例行重活限定窗口跑
//（如 22:00-06:00 夜间重建索引）。跨午夜的窗口自然支持。

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseWindow 解析 "HH:MM-HH:MM"（本地时区）：返回起止分钟数（自零点），
// end < start 即跨午夜窗口。空串返回零值与 nil（不限）。
func ParseWindow(s string) (start, end int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("时间窗须为 HH:MM-HH:MM：%q", s)
	}
	if start, err = parseClock(lo); err != nil {
		return 0, 0, err
	}
	if end, err = parseClock(hi); err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("时刻须为 HH:MM：%q", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("时刻越界：%q", s)
	}
	return hh*60 + mm, nil
}

// InWindow 报告 t（本地时区的时刻）是否在任务窗口内；无窗口即恒真。
// 跨午夜（start > end）取并集段 [start,24:00)∪[0,end)。
func (j Job) InWindow(t time.Time) bool {
	if j.Window == "" {
		return true
	}
	start, end, err := ParseWindow(j.Window)
	if err != nil {
		return true // 坏窗口不拦任务（存储已校验；此处防御）
	}
	m := t.Hour()*60 + t.Minute()
	if start <= end {
		return m >= start && m <= end
	}
	return m >= start || m <= end
}
