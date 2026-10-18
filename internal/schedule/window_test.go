package schedule

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	cases := []struct {
		in        string
		s, e      int
		wantError bool
	}{
		{"", 0, 0, false},
		{"22:00-06:00", 22 * 60, 6 * 60, false},
		{"09:30-18:45", 9*60 + 30, 18*60 + 45, false},
		{"9:00-18:00", 9 * 60, 18 * 60, false}, // 补零缺失也接受（Atoi 宽容）
		{"25:00-06:00", 0, 0, true},
		{"22:00", 0, 0, true},
		{"22:00-06:60", 0, 0, true},
		{"a-b", 0, 0, true},
	}
	for _, c := range cases {
		s, e, err := ParseWindow(c.in)
		if c.wantError {
			if err == nil {
				t.Errorf("%q accepted", c.in)
			}
			continue
		}
		if err != nil || s != c.s || e != c.e {
			t.Errorf("%q = (%d,%d,%v)", c.in, s, e, err)
		}
	}
}

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 19, h, m, 0, 0, time.Local) }
	day := Job{Window: "09:00-18:00"}
	night := Job{Window: "22:00-06:00"} // 跨午夜
	always := Job{}

	cases := []struct {
		j    Job
		t    time.Time
		want bool
	}{
		{always, at(3, 0), true},
		{day, at(12, 0), true},
		{day, at(9, 0), true},  // 含边界
		{day, at(18, 0), true}, // 含边界
		{day, at(8, 59), false},
		{day, at(18, 1), false},
		{night, at(23, 30), true},
		{night, at(2, 0), true},
		{night, at(22, 0), true},
		{night, at(6, 0), true},
		{night, at(12, 0), false},
		{night, at(21, 59), false},
	}
	for i, c := range cases {
		if got := c.j.InWindow(c.t); got != c.want {
			t.Errorf("%d: %s window=%q = %v", i, c.t.Format("15:04"), c.j.Window, got)
		}
	}
}

// TestSchedulerDefersOutsideWindow 窗口外不 fire 且不动 LastRun——窗口
// 一开即补跑（spec 规则 63）。
func TestSchedulerDefersOutsideWindow(t *testing.T) {
	store := NewStore(t.TempDir())
	// 计划每分钟，窗口 09:00-09:05；now=08:59（窗口外）→ 到期不 fire。
	if err := store.Upsert(Job{ID: "w", Schedule: "* * * * *", Prompt: "跑",
		Workspace: t.TempDir(), Window: "09:00-09:05", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	s := NewScheduler(store, runner)
	before := time.Date(2026, 10, 19, 8, 59, 0, 0, time.Local)
	s.now = func() time.Time { return before }
	s.tick(t.Context())
	if runner.count() != 0 {
		t.Fatal("fired outside window")
	}
	jobs, _ := store.List()
	if !jobs[0].LastRun.IsZero() {
		t.Fatal("LastRun moved outside window")
	}
	// 窗口内（09:00）：到期（上一分钟就该跑）即补跑。
	in := time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local)
	s.now = func() time.Time { return in }
	s.tick(t.Context())
	waitFor(t, func() bool { return runner.count() >= 1 })
	if runner.count() < 1 {
		t.Fatal("did not fire once window opened")
	}
}
