package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

func runMemoryCaptured(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	t.Setenv("ZGO_TEST_DATADIR", dir)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runMemory(args)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return code, string(out)
}

// memoryEnv boots a service with one session over ws and one memory entry.
func memoryEnv(t *testing.T) (dir, ws string) {
	t.Helper()
	dir = t.TempDir()
	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ws = t.TempDir()
	if _, err := svc.NewSession(ws); err != nil {
		t.Fatal(err)
	}
	if err := svc.AppendMemory(ws, "部署脚本用 pnpm"); err != nil {
		t.Fatal(err)
	}
	return dir, ws
}

func TestMemoryListShowAddClear(t *testing.T) {
	dir, ws := memoryEnv(t)

	code, out := runMemoryCaptured(t, dir, "list")
	if code != 0 || !strings.Contains(out, "工作区") || !strings.Contains(out, "部署脚本用 pnpm") == false {
		_ = out
	}
	if code != 0 {
		t.Fatalf("list = %d", code)
	}
	// 列表按路径列出（内容不进列表）。
	if !strings.Contains(out, "工作区") {
		t.Fatalf("list header missing:\n%s", out)
	}

	code, out = runMemoryCaptured(t, dir, "show", "--workspace", ws)
	if code != 0 || !strings.Contains(out, "部署脚本用 pnpm") {
		t.Fatalf("show = %d %q", code, out)
	}

	code, _ = runMemoryCaptured(t, dir, "add", "--workspace", ws, "界面走 native UI")
	if code != 0 {
		t.Fatal("add failed")
	}
	_, out = runMemoryCaptured(t, dir, "show", "--workspace", ws)
	if !strings.Contains(out, "界面走 native UI") {
		t.Fatalf("appended entry missing:\n%s", out)
	}

	// clear 幂等，清后再 show 为空。
	code, _ = runMemoryCaptured(t, dir, "clear", "--workspace", ws)
	if code != 0 {
		t.Fatal("clear failed")
	}
	code, _ = runMemoryCaptured(t, dir, "clear", "--workspace", ws)
	if code != 0 {
		t.Fatal("second clear not idempotent")
	}
	_, out = runMemoryCaptured(t, dir, "show", "--workspace", ws)
	if !strings.Contains(out, "暂无记忆") {
		t.Fatalf("after clear = %q", out)
	}
}

func TestMemoryWorkspaceViaSessionPrefix(t *testing.T) {
	dir, _ := memoryEnv(t)
	svc, _ := service.New(dir)
	defer svc.Close()
	info := svc.ListSessions()[0]
	prefix := info.ID[:len(info.ID)-6]

	code, out := runMemoryCaptured(t, dir, "show", "--workspace", prefix)
	if code != 0 || !strings.Contains(out, "部署脚本用 pnpm") {
		t.Fatalf("prefix show = %d %q", code, out)
	}
}

func TestMemoryRequiresWorkspace(t *testing.T) {
	dir, _ := memoryEnv(t)
	if code, _ := runMemoryCaptured(t, dir, "show"); code == 0 {
		t.Fatal("show without workspace accepted")
	}
	if code, _ := runMemoryCaptured(t, dir, "add", "--workspace", ""); code == 0 {
		t.Fatal("add with empty workspace accepted")
	}
}

// TestMemorySearchCLI（spec 规则 119）：search 渲染（按工作区分组）与
// 空查询退出码。
func TestMemorySearchCLI(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	ws := t.TempDir()
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	svc.NewSession(ws)
	os.MkdirAll(filepath.Join(dataDir, "memory"), 0o755)
	memPath := tools.MemoryPath(filepath.Join(dataDir, "memory"), ws)
	os.WriteFile(memPath, []byte("- 偏好：中文交流\n"), 0o600)
	svc.Close()

	code, out := captureMemoryOut(func() int { return runMemory([]string{"search", "偏好"}) })
	if code != 0 || !strings.Contains(out, "偏好：中文交流") {
		t.Fatalf("search = %d\n%s", code, out)
	}
	if code, out = captureMemoryOut(func() int { return runMemory([]string{"search", "无命中词"}) }); code != 0 || !strings.Contains(out, "没有命中") {
		t.Fatalf("no-hit = %d\n%s", code, out)
	}
	if code := runMemory([]string{"search"}); code != 2 {
		t.Fatalf("argless = %d", code)
	}
}

// captureMemoryOut 抓 runMemory 的 stdout。
func captureMemoryOut(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}
