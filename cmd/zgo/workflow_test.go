package main

import (
	"os"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/workflow"
)

// wfCLIEnv redirects the CLI's data dir and plants a definition + a run.
func wfCLIEnv(t *testing.T) *workflow.Store {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	store := workflow.NewStore(dataDir)
	if err := store.Save(workflow.Definition{Name: "nightly-check", Steps: []agent.WorkflowStep{
		{Description: "扫描依赖", Kind: "research"},
		{Name: "fix", Description: "修复", Kind: "edit", After: []string{"scan"}},
	}}); err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun("nightly-check", "D:/ws", []agent.WorkflowStep{
		{Description: "扫描依赖", Kind: "research"},
		{Name: "fix", Description: "修复", Kind: "edit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.FinishRun(run, []agent.StepResult{
		{Step: agent.WorkflowStep{Description: "扫描依赖"}, Text: "ok"},
	}, "failed", "step 2 failed")
	return store
}

func TestWorkflowCLIListShowRuns(t *testing.T) {
	wfCLIEnv(t)

	code, out := captureWorkflowCLI(t, "list")
	if code != 0 || !strings.Contains(out, "nightly-check") || !strings.Contains(out, "2") {
		t.Fatalf("list code=%d out=%q", code, out)
	}

	code, out = captureWorkflowCLI(t, "show", "nightly-check")
	if code != 0 || !strings.Contains(out, "扫描依赖") || !strings.Contains(out, "after:") {
		t.Fatalf("show code=%d out=%q", code, out)
	}

	code, out = captureWorkflowCLI(t, "runs")
	if code != 0 || !strings.Contains(out, "nightly-check") || !strings.Contains(out, "failed") ||
		!strings.Contains(out, "1/2") {
		t.Fatalf("runs code=%d out=%q", code, out)
	}

	if code, _ := captureWorkflowCLI(t, "show", "nope"); code != 1 {
		t.Fatal("unknown name should exit 1")
	}
	if code, _ := captureWorkflowCLI(t, "bogus"); code != 2 {
		t.Fatal("unknown subcommand should exit 2")
	}
}

// captureWorkflowCLI runs the CLI with stdout captured.
func captureWorkflowCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runWorkflow(args)
	w.Close()
	os.Stdout = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return code, string(buf[:n])
}
