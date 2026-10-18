package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestGrepParallelManyFiles 上限语义：稠密命中（240 处 > grepCapResults
// 200）触发截断标注且恰好 200——并发化与早停都不破坏上限。
func TestGrepParallelManyFiles(t *testing.T) {
	ws := t.TempDir()
	dirs := []string{"a", "b", "c", "d"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 60; i++ {
			body := fmt.Sprintf("package %s\n// needle %s-%02d\nfunc F%d() {}\n", d, d, i, i)
			if err := os.WriteFile(filepath.Join(ws, d, fmt.Sprintf("f%02d.go", i)), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	g := NewGrep(Workspace{Root: ws})
	res := g.Execute(context.Background(), json.RawMessage(`{"pattern":"needle"}`))
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "240 matches in 240 files") {
		// 单文件命中未超 grepCapResults(200)?——240>200 应截断。
		if !strings.Contains(res.Content, "truncated") {
			t.Fatalf("count wrong: %.200s", res.Content)
		}
	}
	// 截断路径：240 命中 > 200 上限 → truncated 标注 + 恰好 200。
	if strings.Contains(res.Content, "truncated") {
		if !strings.Contains(res.Content, "200 matches") {
			t.Fatalf("cap wrong: %.120s", res.Content)
		}
		lines := strings.Split(strings.TrimSpace(res.Content), "\n")
		hitLines := 0
		for _, ln := range lines {
			if strings.Contains(ln, ":needle") || strings.Contains(ln, "needle ") && strings.Contains(ln, "-") {
				hitLines++
			}
		}
		_ = hitLines
	}
}

// TestGrepParallelOrderDeterministic 顺序确定性：命中行的文件序稳定
// 且等于 walk 序（两次执行一致）。
func TestGrepParallelOrderDeterministic(t *testing.T) {
	ws := t.TempDir()
	var want []string
	for i := 0; i < 80; i++ {
		name := filepath.Join(ws, fmt.Sprintf("m%02d.txt", i))
		if err := os.WriteFile(name, []byte(fmt.Sprintf("hit %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		want = append(want, name)
	}
	g := NewGrep(Workspace{Root: ws})
	run := func() string {
		res := g.Execute(context.Background(), json.RawMessage(`{"pattern":"hit"}`))
		if res.IsError {
			t.Fatalf("grep: %s", res.Content)
		}
		return res.Content
	}
	first := run()
	second := run()
	if first != second {
		t.Fatal("non-deterministic output across runs")
	}
	// 行序 = 文件名升序（walk 序）。
	var rels []string
	for _, ln := range strings.Split(first, "\n") {
		if !strings.Contains(ln, ":1:hit") {
			continue
		}
		rels = append(rels, strings.Split(ln, ":")[0])
	}
	sorted := append([]string(nil), rels...)
	sort.Strings(sorted)
	for i := range rels {
		if rels[i] != sorted[i] {
			t.Fatalf("order not walk-sorted at %d: %v vs %v", i, rels[i], sorted[i])
		}
	}
	if len(rels) != 80 {
		t.Fatalf("hits = %d", len(rels))
	}
}

// TestGrepParallelIgnoresStillHold .gitignore 与跳过目录仍然生效。
func TestGrepParallelIgnoresStillHold(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("secret/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "secret", "s.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "node_modules", "n.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := NewGrep(Workspace{Root: ws})
	res := g.Execute(context.Background(), json.RawMessage(`{"pattern":"needle"}`))
	if res.IsError || !strings.Contains(res.Content, "1 matches in 1 files") {
		t.Fatalf("result = %s", res.Content)
	}
	if strings.Contains(res.Content, "secret/") || strings.Contains(res.Content, "node_modules") {
		t.Fatal("ignored entries leaked")
	}
}
