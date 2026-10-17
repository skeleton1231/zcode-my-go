package main

import (
	"io"
	"os"
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
