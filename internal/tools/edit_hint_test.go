package tools

import (
	"strings"
	"testing"
)

// TestApplyEditMissHintNearest: 未命中错误附带最近匹配行号与摘录——
// 缩进漂移（仅空白差异）与中段插入两类典型失败（issue #218）。
func TestApplyEditMissHintNearest(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tif true {\n\t\tprintln(\"hi\")\n\t}\n}\n"

	// 缩进漂移：old 用两格缩进，文件里是 tab。
	old := "  if true {\n    println(\"hi\")\n  }\n"
	_, _, reason := applyEdit(content, old, "x", false)
	for _, want := range []string{"最近匹配", "L", "println", "仅空白"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("drift reason missing %q: %q", want, reason)
		}
	}

	// 中段插入：old 少了一行上下文（近似段仍可定位）。
	old2 := "if true {\n\tprintln(\"hi\")\n}"
	_, _, reason2 := applyEdit(content, old2, "x", false)
	if !strings.Contains(reason2, "最近匹配") || !strings.Contains(reason2, "println") {
		t.Fatalf("mid-insert reason = %q", reason2)
	}
}

// TestApplyEditMissHintMultiple: 非唯一命中列出全部行号（封顶）。
func TestApplyEditMissHintMultiple(t *testing.T) {
	content := "a\nb\na\nb\na\nb\na\nb\na\nb\n"
	_, _, reason := applyEdit(content, "a\n", "x", false)
	if !strings.Contains(reason, "L1") || !strings.Contains(reason, "L5") || !strings.Contains(reason, "L9") {
		t.Fatalf("multi reason missing line numbers: %q", reason)
	}
}

// TestApplyEditMissHintCapped: 摘录不膨胀——超长行/大窗口截断。
func TestApplyEditMissHintCapped(t *testing.T) {
	long := strings.Repeat("x", 20000)
	content := "short\n" + long + "\nend\n"
	_, _, reason := applyEdit(content, "nope\n"+long[:100]+"\n", "x", false)
	if len(reason) > 4000 {
		t.Fatalf("hint too large: %d bytes", len(reason))
	}
}
