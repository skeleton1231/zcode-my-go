package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func runPatch(t *testing.T, ws Workspace, patch string) agent.ToolResult {
	t.Helper()
	return NewPatch(ws).Execute(context.Background(), json.RawMessage(
		`{"patch":`+quoteJSON(patch)+`}`))
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPatchAddUpdateDelete(t *testing.T) {
	w := testWorkspace(t)

	res := runPatch(t, w, "*** Begin Patch\n*** Add File: a.txt\n+one\n+two\n+three\n*** End Patch")
	if res.IsError {
		t.Fatalf("add failed: %s", res.Content)
	}
	data, _ := os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("added file = %q", data)
	}

	// 更新：上下文 + 删除 + 新增。
	res = runPatch(t, w, "*** Begin Patch\n*** Update File: a.txt\n@@\n one\n-two\n+TWO\n three\n*** End Patch")
	if res.IsError {
		t.Fatalf("update failed: %s", res.Content)
	}
	data, _ = os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "one\nTWO\nthree\n" {
		t.Fatalf("updated file = %q", data)
	}

	// 多文件 + 删除。
	res = runPatch(t, w, "*** Begin Patch\n*** Add File: d/b.txt\n+hi\n*** Delete File: a.txt\n*** End Patch")
	if res.IsError {
		t.Fatalf("multi failed: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt not deleted")
	}
	data, _ = os.ReadFile(filepath.Join(w.Root, "d", "b.txt"))
	if string(data) != "hi\n" {
		t.Fatalf("b.txt = %q", data)
	}
}

func TestPatchRejectsAmbiguousAndMissing(t *testing.T) {
	w := testWorkspace(t)
	os.WriteFile(filepath.Join(w.Root, "dup.txt"), []byte("x\nx\n"), 0o644)

	// 两处匹配 → 拒绝。
	res := runPatch(t, w, "*** Begin Patch\n*** Update File: dup.txt\n@@\n-x\n+Y\n*** End Patch")
	if !res.IsError || !strings.Contains(res.Content, "2 places") {
		t.Fatalf("ambiguous accepted: %+v", res)
	}
	// 找不到 → 拒绝。
	res = runPatch(t, w, "*** Begin Patch\n*** Update File: dup.txt\n@@\n-nope\n+Y\n*** End Patch")
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Fatalf("missing accepted: %+v", res)
	}
	// 缺 Begin 标记。
	res = runPatch(t, w, "*** Update File: dup.txt\n@@\n-nope\n+Y\n")
	if !res.IsError {
		t.Fatal("unterminated patch accepted")
	}
}

// TestPatchFailsAtomicallyPerFile verifies the report names what applied
// before a failure (like ZCode's per-file rollback reporting).
func TestPatchReportsAppliedBeforeFailure(t *testing.T) {
	w := testWorkspace(t)
	res := runPatch(t, w, "*** Begin Patch\n*** Add File: ok.txt\n+fine\n*** Update File: missing.txt\n@@\n-x\n+y\n*** End Patch")
	if !res.IsError {
		t.Fatal("failing patch accepted")
	}
	if !strings.Contains(res.Content, "added ok.txt") {
		t.Fatalf("partial report missing: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "ok.txt")); err != nil {
		t.Fatal("ok.txt not actually written")
	}
}

func TestPatchStaysInWorkspace(t *testing.T) {
	w := testWorkspace(t)
	res := runPatch(t, w, "*** Begin Patch\n*** Add File: ../escape.txt\n+x\n*** End Patch")
	if !res.IsError {
		t.Fatal("workspace escape accepted")
	}
}
