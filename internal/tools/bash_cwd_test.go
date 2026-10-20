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

// TestBashCwdArg（spec 规则 110）：cwd 参数设定本次运行目录——相对根
// 解析；越界即错误；一次性（不污染粘滞 cwd）；粘滞 cd 后 cwd 参数仍
// 优先。
func TestBashCwdArg(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("no process model on wasm")
	}
	w := testWorkspace(t)
	sub := filepath.Join(w.Root, "sub")
	nested := filepath.Join(sub, "nested")
	os.MkdirAll(nested, 0o755)
	b := NewBash(w, nil)

	res := b.Execute(context.Background(), json.RawMessage(`{"command":"pwd","cwd":"sub/nested"}`))
	if res.IsError {
		t.Fatalf("cwd run: %s", res.Content)
	}
	if !strings.Contains(res.Content, "nested") {
		t.Fatalf("pwd in cwd = %q", res.Content)
	}
	// cwd 结果经哨兵成为新粘滞目录（与 cd 同语义——「从那跑过」即
	// 后续也从那跑）：下一条 pwd 仍在 nested。
	res = b.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if res.IsError || !strings.Contains(res.Content, "nested") {
		t.Fatalf("sticky after cwd arg = %q", res.Content)
	}
	// 越界即错误，不执行命令。
	res = b.Execute(context.Background(), json.RawMessage(`{"command":"pwd","cwd":"../../outside"}`))
	if !res.IsError {
		t.Fatalf("escape accepted: %q", res.Content)
	}
	// 粘滞 cd 后 cwd 参数优先。
	b.Execute(context.Background(), json.RawMessage(`{"command":"cd sub"}`))
	res = b.Execute(context.Background(), json.RawMessage(`{"command":"pwd","cwd":"sub/nested"}`))
	if res.IsError || !strings.Contains(res.Content, "nested") {
		t.Fatalf("cwd vs sticky = %q", res.Content)
	}
}
