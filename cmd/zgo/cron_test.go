package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/schedule"
)

// cronEnv redirects the CLI's data dir to a temp dir and captures stdout.
func cronEnv(t *testing.T) (dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	return dataDir
}

func TestCronAddListRm(t *testing.T) {
	dataDir := cronEnv(t)
	ws := t.TempDir()

	code := runCronArgs(t, "add", "--schedule", "*/5 * * * *",
		"--prompt", "看看状态", "--workspace", ws, "--id", "j1")
	if code != 0 {
		t.Fatalf("add code = %d", code)
	}
	jobs, err := schedule.NewStore(dataDir).List()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %v, %v", jobs, err)
	}
	if jobs[0].ID != "j1" || !jobs[0].Enabled || jobs[0].Workspace != ws {
		t.Fatalf("job = %+v", jobs[0])
	}

	// 非法计划在 add 即被拒绝。
	if code := runCronArgs(t, "add", "--schedule", "bad", "--prompt", "x",
		"--workspace", ws, "--id", "j2"); code == 0 {
		t.Fatal("bad schedule accepted")
	}

	// disable → enable → rm 全链路。
	if code := runCronArgs(t, "disable", "j1"); code != 0 {
		t.Fatalf("disable = %d", code)
	}
	jobs, _ = schedule.NewStore(dataDir).List()
	if jobs[0].Enabled {
		t.Fatal("not disabled")
	}
	if code := runCronArgs(t, "enable", "j1"); code != 0 {
		t.Fatalf("enable = %d", code)
	}
	if code := runCronArgs(t, "rm", "j1"); code != 0 {
		t.Fatalf("rm = %d", code)
	}
	jobs, _ = schedule.NewStore(dataDir).List()
	if len(jobs) != 0 {
		t.Fatalf("jobs after rm = %d", len(jobs))
	}
	if code := runCronArgs(t, "rm", "missing"); code == 0 {
		t.Fatal("rm missing job succeeded")
	}
}

// runCronArgs captures stdout while the command runs.
func runCronArgs(t *testing.T, args ...string) int {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runCron(args)
	w.Close()
	os.Stdout = old
	io.Copy(io.Discard, r) // 排空管道；表格内容不逐字断言
	return code
}

// TestCronAddNaturalLanguage（spec 规则 95）：--at 自然语言建任务——翻
// 译落库为五字段、list 回显原文、--at 与 --schedule 互斥、坏口语报错
// 不落库。
func TestCronAddNaturalLanguage(t *testing.T) {
	dataDir := cronEnv(t)
	ws := t.TempDir()

	code := runCronArgs(t, "add", "--at", "每天下午3点",
		"--prompt", "日报", "--workspace", ws, "--id", "nat1")
	if code != 0 {
		t.Fatalf("add --at failed: %d", code)
	}
	store := schedule.NewStore(dataDir)
	jobs, err := store.List()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %v err = %v", jobs, err)
	}
	if jobs[0].Schedule != "0 15 * * *" {
		t.Fatalf("schedule = %q", jobs[0].Schedule)
	}
	if jobs[0].At != "每天下午3点" {
		t.Fatalf("at origin = %q", jobs[0].At)
	}
	// list 回显原文。
	out := captureCronList(t, dataDir)
	if !strings.Contains(out, "每天下午3点") {
		t.Fatalf("list missing natural phrase:\n%s", out)
	}
	// 互斥与坏口语。
	if code := runCronArgs(t, "add", "--at", "每天 3:00", "--schedule", "0 3 * * *",
		"--prompt", "x", "--workspace", ws); code != 2 {
		t.Fatalf("mutual exclusion = %d", code)
	}
	if code := runCronArgs(t, "add", "--at", "看心情",
		"--prompt", "x", "--workspace", ws); code != 1 {
		t.Fatalf("bad phrase = %d", code)
	}
	jobs, _ = store.List()
	if len(jobs) != 1 {
		t.Fatalf("bad phrase leaked a job: %d", len(jobs))
	}
}

// captureCronList 抓取 cron list 的 stdout。
func captureCronList(t *testing.T, dataDir string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runCron([]string{"list"})
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

// contains 用 strings.Contains 直接内联（既有 helper 与 models.go 撞名）。

// TestCronNextPreview（spec 规则 108）：next 推演落库任务的触发序列；
// --count 生效；缺 id 退出码 2；无任务退出码 1。
func TestCronNextPreview(t *testing.T) {
	dataDir := cronEnv(t)
	ws := t.TempDir()
	store := schedule.NewStore(dataDir)
	store.Upsert(schedule.Job{ID: "hourly", Schedule: "30 * * * *",
		Prompt: "p", Workspace: ws, Enabled: true})

	code, out := captureCronOut(func() int { return runCron([]string{"next", "hourly"}) })
	if code != 0 || !strings.Contains(out, "1.") || !strings.Contains(out, "2.") {
		t.Fatalf("next = %d %q", code, out)
	}
	code, out = captureCronOut(func() int { return runCron([]string{"next", "hourly", "--count=1"}) })
	if code != 0 || strings.Contains(out, "2.") {
		t.Fatalf("count=1 = %d %q", code, out)
	}
	if code := runCron([]string{"next"}); code != 2 {
		t.Fatalf("argless = %d", code)
	}
	if code := runCron([]string{"next", "missing"}); code != 1 {
		t.Fatalf("missing = %d", code)
	}
}

// captureCronOut 抓 runCron 的 stdout。
func captureCronOut(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}
