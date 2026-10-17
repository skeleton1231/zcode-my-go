// Package textdiff renders line-based unified diffs for tool results
// (edit/write/patch show what changed, ZCode's diff surfaces sized for
// Zgo). Pure Go, no dependencies; oversized inputs degrade instead of
// hanging the tool.
package textdiff

import (
	"fmt"
	"strings"
)

// Caps: LCS 的 O(N·M) 表与输出都必须有界——工具输出必须截断。
const (
	MaxLCSCells  = 1 << 20 // 两侧行数乘积上限，超过走粗粒度替换
	MaxDiffLines = 400     // 输出行数上限（含上下文）
	ContextLines = 3
)

// op is one line of the edit script: keep / delete / insert.
type op struct {
	kind byte // '=' '-' '+'
	text string
}

// Unified renders old→new as a unified diff (3 context lines). Empty
// string when there is no change; coarse full-replace when the LCS table
// would be too large; truncated past MaxDiffLines.
func Unified(old, new []byte) string {
	if string(old) == string(new) {
		return ""
	}
	a, b := splitLines(old), splitLines(new)
	if len(a)*len(b) > MaxLCSCells {
		return render(coarse(a, b))
	}
	return render(lcsScript(a, b))
}

// splitLines splits without trailing newlines; a trailing newline does not
// create an extra empty line.
func splitLines(b []byte) []string {
	s := string(b)
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// lcsScript diffs by LCS backtrack（O(N·M) 时间/空间，调用方已限规模）.
func lcsScript(a, b []string) []op {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	script := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			script = append(script, op{'=', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			script = append(script, op{'-', a[i]})
			i++
		default:
			script = append(script, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		script = append(script, op{'-', a[i]})
	}
	for ; j < m; j++ {
		script = append(script, op{'+', b[j]})
	}
	return script
}

// coarse 是超规模输入的降级：整块删除 + 整块新增。
func coarse(a, b []string) []op {
	script := make([]op, 0, len(a)+len(b))
	for _, l := range a {
		script = append(script, op{'-', l})
	}
	for _, l := range b {
		script = append(script, op{'+', l})
	}
	return script
}

// render groups the script into unified hunks（变更行向两侧各扩
// ContextLines 的上下文，相邻组的上下文重叠即合并）并截断输出。
func render(script []op) string {
	interesting := make([]bool, len(script))
	any := false
	for i, o := range script {
		if o.kind != '=' {
			any = true
			for k := max(i-ContextLines, 0); k <= min(i+ContextLines, len(script)-1); k++ {
				interesting[k] = true
			}
		}
	}
	if !any {
		return ""
	}
	var bld strings.Builder
	written := 0
	i := 0
	for i < len(script) {
		if !interesting[i] {
			i++
			continue
		}
		lo := i
		for i < len(script) && interesting[i] {
			i++
		}
		hi := i
		oldStart, newStart := 1, 1
		for _, o := range script[:lo] {
			switch o.kind {
			case '=':
				oldStart++
				newStart++
			case '-':
				oldStart++
			case '+':
				newStart++
			}
		}
		oldCount, newCount := 0, 0
		for _, o := range script[lo:hi] {
			switch o.kind {
			case '=':
				oldCount++
				newCount++
			case '-':
				oldCount++
			case '+':
				newCount++
			}
		}
		fmt.Fprintf(&bld, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, o := range script[lo:hi] {
			if written >= MaxDiffLines {
				bld.WriteString("… diff 截断 …\n")
				return bld.String()
			}
			if o.kind == '=' {
				bld.WriteByte(' ') // unified 格式：上下文行以空格开头
			} else {
				bld.WriteByte(o.kind)
			}
			bld.WriteString(o.text)
			bld.WriteByte('\n')
			written++
		}
	}
	return bld.String()
}

func max(n, m int) int {
	if n > m {
		return n
	}
	return m
}

func min(n, m int) int {
	if n < m {
		return n
	}
	return m
}
