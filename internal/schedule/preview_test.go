package schedule

// cron 预演测试（spec 规则 108）：无窗口任务的逐次推进、窗口外顺延
// 到开点、跨午夜窗口、disabled 零结果、推演次数上限。

import (
	"testing"
	"time"
)

func TestPreviewNextPlain(t *testing.T) {
	job := Job{ID: "j", Schedule: "0 9 * * *", Enabled: true}
	base := time.Date(2026, 10, 19, 10, 0, 0, 0, time.Local)
	got := PreviewNext(job, base, 3)
	if len(got) != 3 {
		t.Fatalf("n = %d", len(got))
	}
	want := []string{"2026-10-20 09:00", "2026-10-21 09:00", "2026-10-22 09:00"}
	for i, w := range want {
		if got[i].Format("2006-01-02 15:04") != w {
			t.Fatalf("[%d] = %s want %s", i, got[i].Format("2006-01-02 15:04"), w)
		}
	}
}

func TestPreviewNextWindowDefers(t *testing.T) {
	// 每天 3 点到期，但窗口 09:00-18:00：3 点在窗口外 → 顺延到当日
	// 09:00（窗口一开即补跑）；之后每次仍 3 点到期再顺延次日 9 点。
	job := Job{ID: "j", Schedule: "0 3 * * *", Window: "09:00-18:00", Enabled: true}
	base := time.Date(2026, 10, 19, 1, 0, 0, 0, time.Local)
	got := PreviewNext(job, base, 2)
	if len(got) != 2 {
		t.Fatalf("n = %d", len(got))
	}
	if s := got[0].Format("2006-01-02 15:04"); s != "2026-10-19 09:00" {
		t.Fatalf("[0] = %s", s)
	}
	if s := got[1].Format("2006-01-02 15:04"); s != "2026-10-20 09:00" {
		t.Fatalf("[1] = %s", s)
	}
	// 窗口内到期（15 点，窗口 09-18）：正常逐日。
	job2 := Job{ID: "j2", Schedule: "0 15 * * *", Window: "09:00-18:00", Enabled: true}
	got2 := PreviewNext(job2, base, 2)
	if got2[0].Format("2006-01-02 15:04") != "2026-10-19 15:00" {
		t.Fatalf("in-window [0] = %s", got2[0].Format("2006-01-02 15:04"))
	}
}

func TestPreviewNextCrossMidnight(t *testing.T) {
	// 跨午夜窗口 22:00-06:00：凌晨 3 点在窗口内（fire 不顺延）。
	job := Job{ID: "j", Schedule: "0 3 * * *", Window: "22:00-06:00", Enabled: true}
	base := time.Date(2026, 10, 19, 1, 0, 0, 0, time.Local)
	got := PreviewNext(job, base, 1)
	if got[0].Format("2006-01-02 15:04") != "2026-10-19 03:00" {
		t.Fatalf("cross-midnight in-window = %s", got[0].Format("2006-01-02 15:04"))
	}
	// 12 点到期（窗口外）→ 顺延到 22:00 开点。
	job2 := Job{ID: "j2", Schedule: "0 12 * * *", Window: "22:00-06:00", Enabled: true}
	got2 := PreviewNext(job2, base, 1)
	if got2[0].Format("2006-01-02 15:04") != "2026-10-19 22:00" {
		t.Fatalf("cross-midnight defer = %s", got2[0].Format("2006-01-02 15:04"))
	}
}

func TestPreviewNextEdges(t *testing.T) {
	base := time.Date(2026, 10, 19, 1, 0, 0, 0, time.Local)
	if got := PreviewNext(Job{ID: "d", Schedule: "* * * * *", Enabled: false}, base, 3); got != nil {
		t.Fatalf("disabled = %v", got)
	}
	if got := PreviewNext(Job{ID: "b", Schedule: "bad", Enabled: true}, base, 3); got != nil {
		t.Fatalf("bad schedule = %v", got)
	}
	if got := PreviewNext(Job{ID: "o", Schedule: "* * * * *", Enabled: true}, base, 0); got != nil {
		t.Fatalf("n<=0 = %v", got)
	}
}
