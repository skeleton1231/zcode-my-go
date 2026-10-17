package schedule

import (
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestParseCronValid(t *testing.T) {
	cases := []struct {
		expr  string
		field int // 校验字段
		want  map[int]bool
	}{
		{"*/5 * * * *", 0, map[int]bool{0: true, 5: true, 55: true, 3: false}},
		{"0 9 * * 1-5", 4, map[int]bool{1: true, 5: true, 0: false, 6: false}},
		{"30 8 1 * *", 2, map[int]bool{1: true, 31: false}},
		{"0 0 29 2 *", 3, map[int]bool{2: true, 3: false}},
		{"7,19 * * * *", 0, map[int]bool{7: true, 19: true, 8: false}},
		{"0 12 * * 7", 4, map[int]bool{0: true, 7: false}}, // 7 折叠为周日
		{"10-14 * * * *", 0, map[int]bool{10: true, 14: true, 9: false, 15: false}},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			s, err := ParseCron(tc.expr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			for v, want := range tc.want {
				if s.fields[tc.field][v] != want {
					t.Fatalf("field %d value %d: got %v", tc.field, v, !want)
				}
			}
		})
	}
}

func TestParseCronInvalid(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *",
		"* * 0 * *", "* * * 13 *", "*/0 * * * *", "5-2 * * * *",
		"a * * * *", "1/2 * * * *", "* * * * 8", ", * * * *",
	} {
		if _, err := ParseCron(expr); err == nil {
			t.Fatalf("%q accepted", expr)
		}
	}
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr, from, want string
	}{
		// 同分钟内：from 之后的下一个匹配。
		{"*/5 * * * *", "2026-10-18 10:03", "2026-10-18 10:05"},
		{"*/5 * * * *", "2026-10-18 10:05", "2026-10-18 10:10"},
		{"0 9 * * *", "2026-10-18 10:00", "2026-10-19 09:00"},
		{"0 9 * * 1-5", "2026-10-18 10:00", "2026-10-19 09:00"},   // 周一
		{"0 9 * * 6,0", "2026-10-19 10:00", "2026-10-24 09:00"},   // 下周六
		{"30 8 1 * *", "2026-10-18 10:00", "2026-11-01 08:30"},    // 下月 1 号
		{"59 23 31 12 *", "2026-10-18 10:00", "2026-12-31 23:59"}, // 跨月跳
	}
	for _, tc := range cases {
		t.Run(tc.expr+"@"+tc.from, func(t *testing.T) {
			s, err := ParseCron(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			got := s.Next(at(t, tc.from))
			if got != at(t, tc.want) {
				t.Fatalf("Next(%s) = %s, want %s", tc.from, got.Format("2006-01-02 15:04"), tc.want)
			}
		})
	}
	// 恒不匹配（2 月 31 日）：零值，不死循环。
	s, _ := ParseCron("0 0 31 2 *")
	if next := s.Next(at(t, "2026-10-18 10:00")); !next.IsZero() {
		t.Fatalf("impossible schedule returned %s", next)
	}
}
