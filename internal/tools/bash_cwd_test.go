package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBashCwdSticky: cd 跨调用粘滞（issue #221）——下一次 bash 从上次
// cd 的目录启动；sentinel 标记不泄漏进工具输出。
func TestBashCwdSticky(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("no process model on wasm")
	}
	w := testWorkspace(t)
	sub := filepath.Join(w.Root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := NewBash(w, nil)

	if res := b.Execute(context.Background(), json.RawMessage(`{"command":"cd sub"}`)); res.IsError {
		t.Fatalf("cd: %s", res.Content)
	}
	if strings.Contains(res0(b), cwdMarker) {
		t.Fatalf("marker leaked: %q", res0(b))
	}
	res := b.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if res.IsError {
		t.Fatalf("pwd: %s", res.Content)
	}
	if !strings.Contains(res.Content, "sub") || strings.Contains(res.Content, cwdMarker) {
		t.Fatalf("pwd after cd = %q", res.Content)
	}
}

// TestBashCwdEscapeRejected: cd 出工作区根不持久——下一条仍从根启动
// （issue #221：越狱沿用 jail 语义）。
func TestBashCwdEscapeRejected(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("no process model on wasm")
	}
	w := testWorkspace(t)
	b := NewBash(w, nil)
	if res := b.Execute(context.Background(), json.RawMessage(`{"command":"cd ."}`)); res.IsError {
		t.Fatalf("cd .: %s", res.Content)
	}
	// cd .. 出根（temp 目录的父级）——不持久。
	b.Execute(context.Background(), json.RawMessage(`{"command":"cd .."}`))
	res := b.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if res.IsError {
		t.Fatalf("pwd: %s", res.Content)
	}
	got := filepath.ToSlash(strings.TrimSpace(lineOf(res.Content)))
	if !strings.Contains(strings.ToLower(got), strings.ToLower(filepath.Base(w.Root))) {
		t.Fatalf("cwd escaped root: %q (root base %q)", got, filepath.Base(w.Root))
	}
}

// res0 取粘滞路径下第一条命令的结果文本（便利断言用）。
func res0(b *Bash) string { return b.lastOut }

// lineOf 取多行输出的最后一行。
func lineOf(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
