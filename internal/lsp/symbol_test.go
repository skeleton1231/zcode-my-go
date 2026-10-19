package lsp

// workspace/symbol 渲染测试（spec 规则 99）：SymbolInformation 形态、
// 无命中 null、kind 标签。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderSymbols(t *testing.T) {
	root := t.TempDir()
	raw := json.RawMessage(`[
		{"name":"ParsePos","kind":12,"location":{"uri":"file:///w/util.go","range":{"start":{"line":13,"character":0},"end":{"line":13,"character":9}}}},
		{"name":"Workspace","kind":23,"location":{"uri":"file:///w/types.go","range":{"start":{"line":4,"character":5},"end":{"line":4,"character":14}}}},
		{"name":"NoLocation","kind":13}
	]`)
	lines, err := renderSymbols(root, raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 { // 无 location 的跳过
		t.Fatalf("lines = %v", lines)
	}
	if !strings.Contains(lines[0], "ParsePos Function") || !strings.Contains(lines[0], "util.go:14:1") {
		t.Fatalf("func line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "Workspace Struct") {
		t.Fatalf("struct line = %q", lines[1])
	}
	// null 无命中。
	lines, err = renderSymbols(root, json.RawMessage(`null`), "")
	if err != nil || len(lines) != 0 {
		t.Fatalf("null = %v %v", lines, err)
	}
	// 坏形态报错。
	if _, err := renderSymbols(root, json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("bad shape accepted")
	}
}

func TestSymbolKindLabel(t *testing.T) {
	if symbolKindLabel(12) != "Function" || symbolKindLabel(23) != "Struct" ||
		symbolKindLabel(6) != "Method" || symbolKindLabel(11) != "Interface" {
		t.Fatal("known kinds wrong")
	}
	if symbolKindLabel(99) != "kind99" {
		t.Fatal("unknown kind not labeled")
	}
}
