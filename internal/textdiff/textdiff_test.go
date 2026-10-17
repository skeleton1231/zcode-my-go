package textdiff

import (
	"strings"
	"testing"
)

func TestUnifiedBasics(t *testing.T) {
	cases := []struct {
		name       string
		old, new   string
		wantHas    []string // 必须出现的行
		wantAbsent []string // 必须不出现的行
	}{
		{"identical", "a\nb\n", "a\nb\n", nil, []string{"-a", "+b", "@@"}},
		{"insert", "a\nc\n", "a\nb\nc\n", []string{"+b", " a", " c"}, []string{"-a"}},
		{"delete", "a\nb\nc\n", "a\nc\n", []string{"-b"}, []string{"+b"}},
		{"replace", "hello\nworld\n", "hello\nGo\n", []string{"-world", "+Go"}, nil},
		{"empty to content", "", "x\ny\n", []string{"+x", "+y"}, []string{"-"}},
		{"content to empty", "x\ny\n", "", []string{"-x", "-y"}, []string{"+"}},
		{"no trailing newline", "a", "b", []string{"-a", "+b"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Unified([]byte(tc.old), []byte(tc.new))
			for _, w := range tc.wantHas {
				if !strings.Contains(got, w+"\n") {
					t.Fatalf("diff misses %q:\n%s", w, got)
				}
			}
			for _, w := range tc.wantAbsent {
				if strings.Contains(got, w+"\n") {
					t.Fatalf("diff unexpectedly has %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestUnifiedHunkSplitting: two far-apart changes make two hunks, each
// with its own header and only its own context.
func TestUnifiedHunkSplitting(t *testing.T) {
	var oldB, newB strings.Builder
	for i := 1; i <= 30; i++ {
		fmt := func(w *strings.Builder) {
			w.WriteString(string(rune('a' + i%26)))
			w.WriteString(string(rune('0' + i%10)))
			w.WriteString("\n")
		}
		fmt(&oldB)
		if i == 3 || i == 27 {
			newB.WriteString("CHANGED\n")
		} else {
			fmt(&newB)
		}
	}
	got := Unified([]byte(oldB.String()), []byte(newB.String()))
	if n := strings.Count(got, "\n@@ -") + 1; n != 2 { // 首个 hunk 头前无换行
		t.Fatalf("hunks = %d:\n%s", n, got)
	}
	if !strings.Contains(got, "-d3\n+CHANGED\n") {
		t.Fatalf("first change missing:\n%s", got)
	}
	if !strings.Contains(got, "-b7\n+CHANGED\n") {
		t.Fatalf("second change missing:\n%s", got)
	}
	// 两 hunk 之间不包含中间行（d4…a6 等不出现）。
	if strings.Contains(got, " d4") {
		t.Fatalf("context leaked between hunks:\n%s", got)
	}
}

// TestUnifiedCaps: huge inputs take the coarse path and the output is
// still bounded.
func TestUnifiedCaps(t *testing.T) {
	line := strings.Repeat("x", 100) + "\n"
	huge := strings.Repeat(line, 1500) // 1500 行，与新文件乘积超上限
	got := Unified([]byte(huge), []byte(huge))
	if got != "" {
		t.Fatalf("identical huge input should be empty, got %d bytes", len(got))
	}
	other := strings.Repeat(strings.Repeat("y", 100)+"\n", 1500)
	got = Unified([]byte(huge), []byte(other))
	if lines := strings.Count(got, "\n"); lines > MaxDiffLines+2 {
		t.Fatalf("coarse output = %d lines", lines)
	}
	if !strings.Contains(got, "… diff 截断 …") {
		t.Fatalf("truncation marker missing:\n%.200s", got)
	}
}
