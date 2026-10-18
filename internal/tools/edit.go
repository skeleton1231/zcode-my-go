package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
	"github.com/skeleton1231/zcode-my-go/internal/textdiff"
)

// Edit replaces an exact, unique string in a file — the safest small-change
// primitive: what the model matched is exactly what changes.
type Edit struct{ ws Workspace }

// NewEdit builds the edit tool.
func NewEdit(ws Workspace) *Edit { return &Edit{ws: ws} }

// Name implements agent.Tool.
func (e *Edit) Name() string { return "edit" }

// Description implements agent.Tool.
func (e *Edit) Description() string {
	return "Replace an exact string in a file. old_string must match the file " +
		"content exactly (including whitespace) and be unique unless replace_all " +
		"is true. Read the file first to copy the exact text."
}

// Parameters implements agent.Tool.
func (e *Edit) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":        prop("string", "File path inside the workspace"),
		"old_string":  prop("string", "Exact text to replace, unique in the file"),
		"new_string":  prop("string", "Replacement text"),
		"replace_all": prop("boolean", "Replace every occurrence (default false)"),
	}, "path", "old_string", "new_string")
}

// Execute implements agent.Tool.
func (e *Edit) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.OldString == "" {
		return errResult("old_string is empty; write the file instead")
	}
	if a.OldString == a.NewString {
		return errResult("old_string and new_string are identical")
	}
	abs, err := e.ws.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}

	// 远程工作区：读、改、写全部经 RemoteFS（review #83：曾落回本地
	// 文件系统操作，远程会话上写本地路径）。
	if e.ws.IsRemote() {
		rel := remoteRel(e.ws, abs)
		data, rerr := e.ws.Remote.ReadFile(rel)
		if rerr != nil {
			return errResult("reading %s: %v", a.Path, rerr)
		}
		updated, count, merr := applyEdit(string(data), a.OldString, a.NewString, a.ReplaceAll)
		if merr != "" {
			return errResult("%s in %s", merr, a.Path)
		}
		if werr := e.ws.Remote.WriteFile(rel, []byte(updated), 0o644); werr != nil {
			return errResult("writing %s: %v", a.Path, werr)
		}
		return agent.ToolResult{Content: fmt.Sprintf("replaced %d occurrence(s) in %s\n%s",
			count, a.Path, snippet(updated, a.NewString)),
			Diff: textdiff.Unified(data, []byte(updated))}
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return errResult("reading %s: %v", a.Path, err)
	}
	content := string(data)
	count := strings.Count(content, a.OldString)
	switch {
	case count == 0:
		return errResult("old_string not found in %s; read the file and copy the text exactly", a.Path)
	case count > 1 && !a.ReplaceAll:
		return errResult("old_string appears %d times in %s; add surrounding lines to make it unique or set replace_all", count, a.Path)
	}

	updated := strings.Replace(content, a.OldString, a.NewString, 1)
	if a.ReplaceAll {
		updated = strings.ReplaceAll(content, a.OldString, a.NewString)
	}
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return errResult("writing %s: %v", a.Path, err)
	}
	return agent.ToolResult{Content: fmt.Sprintf("replaced %d occurrence(s) in %s\n%s",
		count, a.Path, snippet(updated, a.NewString)),
		Diff: textdiff.Unified(data, []byte(updated))}
}

// applyEdit 在 content 上执行精确替换，返回更新后内容、命中次数与
// 失败原因（空串为成功）。本地与远程路径共用。
func applyEdit(content, old, new string, replaceAll bool) (string, int, string) {
	count := strings.Count(content, old)
	switch {
	case count == 0:
		return "", 0, "old_string not found; read the file and copy the text exactly" + editMissHint(content, old)
	case count > 1 && !replaceAll:
		return "", count, "old_string appears multiple times; add surrounding lines to make it unique or set replace_all" + editMissHint(content, old)
	}
	if replaceAll {
		return strings.ReplaceAll(content, old, new), count, ""
	}
	return strings.Replace(content, old, new, 1), count, ""
}

// snippet shows the replacement with one line of context around it.
func snippet(content, needle string) string {
	idx := strings.Index(content, needle)
	if idx < 0 {
		return ""
	}
	start := strings.LastIndexByte(content[:idx], '\n')
	if start < 0 {
		start = 0
	} else {
		start++
	}
	endRel := idx + len(needle)
	if endRel > len(content) {
		endRel = len(content)
	}
	end := strings.IndexByte(content[endRel:], '\n')
	if end < 0 {
		end = len(content)
	} else {
		end = endRel + end + 1
	}
	return fmt.Sprintf("---\n%s---", content[start:end])
}

// PreviewDiff implements agent.PreviewDiffer: the change this call would
// make, computed without touching the file（审批者所见即所得）.
func (e *Edit) PreviewDiff(args json.RawMessage) string {
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return ""
	}
	abs, err := e.ws.Resolve(a.Path)
	if err != nil {
		return ""
	}
	var old []byte
	if e.ws.IsRemote() {
		old, _ = e.ws.Remote.ReadFile(remoteRel(e.ws, abs))
	} else {
		old, _ = os.ReadFile(abs)
	}
	updated, _, merr := applyEdit(string(old), a.OldString, a.NewString, a.ReplaceAll)
	if merr != "" {
		return ""
	}
	return textdiff.Unified(old, []byte(updated))
}

// Capability implements agent.Capabler: editing changes one file at a time.
func (e *Edit) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// PathKey implements agent.PathGroupable: edit 是读-改-写语义，同路径
// 的并行调用必须顺序执行（issue #78）。
func (e *Edit) PathKey(args string) (string, bool) {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return "", false
	}
	return a.Path, true
}


// editHint 扫描/摘录的封顶（错误消息不膨胀——超长行、超大窗口一律截
// 断，输出上限教义）。
const (
	editHintMaxLines = 20000 // 超大文件的 nearest 扫描熔断
	editHintMaxWin   = 200   // old 超过此行数不做窗口比对
	editHintLines    = 4     // 摘录行数
	editHintLineCap  = 120   // 摘录单行字节
	editHintHits     = 8     // 多处命中列出的位置数
)

// editMissHint 为未命中/非唯一构造自纠线索（issue #218：错误消息即自
// 纠燃料——最近匹配行号 + 摘录；多处命中列行号；仅空白差异点名）。
func editMissHint(content, old string) string {
	if strings.Contains(content, old) {
		at := editMatchLines(content, old, editHintHits)
		parts := make([]string, len(at))
		for i, l := range at {
			parts[i] = "L" + strconv.Itoa(l)
		}
		return "；命中位置：" + strings.Join(parts, ", ")
	}
	s, e, ex, tr, exc := editNearest(content, old)
	if e == 0 {
		return ""
	}
	hint := fmt.Sprintf("；最近匹配 L%d-%d：%s", s, e, exc)
	if tr > 0 && ex == 0 {
		hint += "（仅空白/缩进不同——old_string 的空白与文件不一致）"
	}
	return hint
}

// editMatchLines 报告 old 各命中的 1 基行号（封顶 cap）。
func editMatchLines(content, old string, cap int) []int {
	var out []int
	off := 0
	for len(out) < cap {
		i := strings.Index(content[off:], old)
		if i < 0 {
			break
		}
		abs := off + i
		out = append(out, 1+strings.Count(content[:abs], "\n"))
		off = abs + len(old)
	}
	return out
}

// editNearest 找与 old 最相似的连续行窗口：返回起止行、逐行精确/去空
// 白命中数与摘录。文件或 old 过大直接放弃（e=0）。
func editNearest(content, old string) (start, end, exact, trimmed int, excerpt string) {
	lines := strings.Split(content, "\n")
	oldLines := strings.Split(old, "\n")
	n := len(oldLines)
	if n == 0 || len(lines) == 0 || n > editHintMaxWin || len(lines) > editHintMaxLines {
		return 0, 0, 0, 0, ""
	}
	best, bestEx, bestTr := -1, -1, -1
	for i := 0; i+n <= len(lines); i++ {
		ex, tr := 0, 0
		for j := 0; j < n; j++ {
			if oldLines[j] == "" {
				continue // 空行平凡匹配：不进信号（否则遮蔽"仅空白不同"）
			}
			if lines[i+j] == oldLines[j] {
				ex++
			}
			if strings.TrimSpace(lines[i+j]) == strings.TrimSpace(oldLines[j]) {
				tr++
			}
		}
		if ex > bestEx || (ex == bestEx && tr > bestTr) {
			best, bestEx, bestTr = i, ex, tr
		}
	}
	if best < 0 {
		return 0, 0, 0, 0, ""
	}
	lo, hi := best, best+n
	if lo > 0 {
		lo--
	}
	if hi < len(lines) {
		hi++
	}
	if hi-lo > editHintLines+2 {
		hi = lo + editHintLines + 2
	}
	var parts []string
	for _, l := range lines[lo:hi] {
		if len(l) > editHintLineCap {
			l = l[:editHintLineCap] + "…"
		}
		parts = append(parts, l)
	}
	return lo + 1, hi, bestEx, bestTr, "「" + strings.Join(parts, "⏎") + "」"
}
