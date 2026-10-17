package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// runBash 执行 bash 工具并返回结果与耗时。
func runBash(t *testing.T, ctx context.Context, timeoutMS int, command string) (agent.ToolResult, time.Duration) {
	t.Helper()
	b := NewBash(Workspace{Root: t.TempDir()}, NewJobs())
	start := time.Now()
	res := b.Execute(ctx, mustArgs(t, map[string]any{"command": command, "timeout_ms": timeoutMS}))
	return res, time.Since(start)
}

// TestBashTimeoutReportsTimeout 验证超时被如实报告为 timed out 且携带
// partial output（issue #6：Windows 上曾误报 exit status 1、输出全丢）。
func TestBashTimeoutReportsTimeout(t *testing.T) {
	command := "echo partial; sleep 30"
	if runtime.GOOS == "windows" {
		command = "Write-Output partial; Start-Sleep -Seconds 30"
	}
	res, elapsed := runBash(t, context.Background(), 1500, command)
	if !res.IsError {
		t.Fatalf("timeout must be an error result: %+v", res)
	}
	if !strings.Contains(res.Content, "timed out") {
		t.Fatalf("content = %q, want \"timed out\"", res.Content)
	}
	if !strings.Contains(res.Content, "partial") {
		t.Fatalf("partial output lost: %q", res.Content)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("timeout not enforced, took %s", elapsed)
	}
}

// TestBashAbortReportsCanceled 验证父 ctx 取消（abort）报告 canceled
// 而非 exit status（issue #6）。
func TestBashAbortReportsCanceled(t *testing.T) {
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 30"
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()
	res, _ := runBash(t, ctx, 30000, command)
	if !res.IsError || !strings.Contains(res.Content, "canceled") {
		t.Fatalf("content = %q, want canceled", res.Content)
	}
}

// TestBashGrandchildKilledOnTimeout 验证超时击杀覆盖整棵进程树：
// 父命令派生的孙进程在超时后不得完成写入（issue #7：Windows 实测
// Start-Process 孙进程活过击杀）。
func TestBashGrandchildKilledOnTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	var command string
	if runtime.GOOS == "windows" {
		// 孙进程 5s 后写 marker，父进程睡 60s 拖到超时。
		command = "Start-Process powershell -ArgumentList ('-NoProfile','-Command','Start-Sleep 5; Set-Content -Path " +
			marker + " -Value lived') -WindowStyle Hidden; Start-Sleep -Seconds 60"
	} else {
		command = "(sleep 5; echo lived > " + marker + ") & sleep 60"
	}
	b := NewBash(Workspace{Root: root}, NewJobs())
	res := b.Execute(context.Background(), mustArgs(t, map[string]any{"command": command, "timeout_ms": 2000}))
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Fatalf("content = %q, want timed out", res.Content)
	}
	// 孙进程本该在 5s 时写 marker；等待超过该时刻后必须仍不存在。
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		data, _ := os.ReadFile(marker)
		t.Fatalf("grandchild survived the kill: marker=%q", data)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

// mustArgs 构造工具参数 JSON。
func mustArgs(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
