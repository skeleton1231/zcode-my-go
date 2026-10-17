package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Patch applies one multi-file patch in Claude Code / ZCode's ApplyPatch
// format (their tool/patch-parser.ts + patch-apply.ts), translated:
//
//	*** Begin Patch
//	*** Add File: path
//	+line
//	*** Update File: path
//	@@ optional anchor
//	 context
//	-old
//	+new
//	*** Delete File: path
//	*** End Patch
type Patch struct{ ws Workspace }

// NewPatch builds the apply-patch tool.
func NewPatch(ws Workspace) *Patch { return &Patch{ws: ws} }

// Name implements agent.Tool.
func (p *Patch) Name() string { return "apply_patch" }

// Description implements agent.Tool.
func (p *Patch) Description() string {
	return "Apply a multi-file patch in ApplyPatch format: Add File, Update File " +
		"(context/-old/+new hunks, unique match required) and Delete File " +
		"sections between Begin/End markers. Read files before updating them."
}

// Parameters implements agent.Tool.
func (p *Patch) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"patch": prop("string", "The full ApplyPatch text"),
	}, "patch")
}

// Execute implements agent.Tool.
func (p *Patch) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Patch string `json:"patch"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	ops, err := ParsePatch(a.Patch)
	if err != nil {
		return errResult("%v", err)
	}
	if len(ops) == 0 {
		return errResult("patch has no file sections")
	}
	var report []string
	for _, op := range ops {
		line, isErr := p.apply(op)
		if isErr {
			return agent.ToolResult{IsError: true, Content: fmt.Sprintf(
				"error: %s\n已应用的改动：\n%s", line, strings.Join(report, "\n"))}
		}
		report = append(report, line)
	}
	return agent.ToolResult{Content: strings.Join(report, "\n")}
}

// apply runs one section and returns its report line.
func (p *Patch) apply(op PatchOp) (string, bool) {
	abs, err := p.ws.Resolve(op.Path)
	if err != nil {
		return fmt.Sprintf("%s: %v", op.Path, err), true
	}
	switch op.Kind {
	case PatchAdd:
		if _, err := os.Stat(abs); err == nil {
			return fmt.Sprintf("%s: already exists", op.Path), true
		}
		if dir := filepath.Dir(abs); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Sprintf("%s: %v", op.Path, err), true
			}
		}
		if err := os.WriteFile(abs, []byte(strings.Join(op.Add, "\n")+"\n"), 0o644); err != nil {
			return fmt.Sprintf("%s: %v", op.Path, err), true
		}
		return fmt.Sprintf("added %s (%d lines)", op.Path, len(op.Add)), false

	case PatchDelete:
		if err := os.Remove(abs); err != nil {
			return fmt.Sprintf("%s: %v", op.Path, err), true
		}
		return "deleted " + op.Path, false

	default: // update
		data, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Sprintf("%s: %v", op.Path, err), true
		}
		updated, applied, err := ApplyHunks(string(data), op.Hunks)
		if err != nil {
			return fmt.Sprintf("%s: %v", op.Path, err), true
		}
		if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
			return fmt.Sprintf("%s: %v", op.Path, err), true
		}
		return fmt.Sprintf("updated %s (%d hunks)", op.Path, applied), false
	}
}

// Capability implements agent.Capabler: patches change files, but through
// one auditable, match-checked operation.
func (p *Patch) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// --- patch parsing and application ----------------------------------------

// Patch kinds.
const (
	PatchAdd    = "add"
	PatchUpdate = "update"
	PatchDelete = "delete"
)

// PatchHunk is one update hunk: the lines to find (context plus removals)
// and the lines to put in their place (context plus additions).
type PatchHunk struct {
	Find    []string // context + "-" lines, in order
	Replace []string // context + "+" lines, in order
}

// PatchOp is one file section of a patch.
type PatchOp struct {
	Kind  string
	Path  string
	Add   []string // PatchAdd: the file's lines
	Hunks []PatchHunk
}

// ParsePatch reads ApplyPatch text into operations.
func ParsePatch(text string) ([]PatchOp, error) {
	var ops []PatchOp
	var cur *PatchOp
	var hunk *PatchHunk
	started := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.TrimSpace(line) == "*** Begin Patch":
			started = true
		case strings.TrimSpace(line) == "*** End Patch":
			return ops, nil
		case strings.HasPrefix(line, "*** Add File: "):
			ops = append(ops, PatchOp{Kind: PatchAdd, Path: strings.TrimPrefix(line, "*** Add File: ")})
			cur = &ops[len(ops)-1]
			hunk = nil
		case strings.HasPrefix(line, "*** Update File: "):
			ops = append(ops, PatchOp{Kind: PatchUpdate, Path: strings.TrimPrefix(line, "*** Update File: ")})
			cur = &ops[len(ops)-1]
			hunk = nil
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, PatchOp{Kind: PatchDelete, Path: strings.TrimPrefix(line, "*** Delete File: ")})
			cur = &ops[len(ops)-1]
			hunk = nil
		case !started:
			continue // 容忍 patch 前的说明文字
		case strings.HasPrefix(line, "@@"):
			if cur == nil || cur.Kind != PatchUpdate {
				return nil, fmt.Errorf("@@ outside an Update File section")
			}
			cur.Hunks = append(cur.Hunks, PatchHunk{})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
		case strings.HasPrefix(line, "+"):
			if cur == nil {
				return nil, fmt.Errorf("+ line outside a file section")
			}
			if cur.Kind == PatchAdd {
				cur.Add = append(cur.Add, strings.TrimPrefix(line, "+"))
			} else if hunk != nil {
				hunk.Replace = append(hunk.Replace, strings.TrimPrefix(line, "+"))
			} else {
				return nil, fmt.Errorf("+ line before any @@ in %s", cur.Path)
			}
		case strings.HasPrefix(line, "-"):
			if cur == nil || cur.Kind != PatchUpdate || hunk == nil {
				return nil, fmt.Errorf("- line outside an update hunk")
			}
			hunk.Find = append(hunk.Find, strings.TrimPrefix(line, "-"))
			// 删除行的位置也要在 Replace 中占位吗？不：Replace 只含保留与新增。
		case strings.TrimSpace(line) == "":
			// 空行在 hunk 中是上下文（真空行与前导空格占位都算）。
			if hunk != nil {
				hunk.Find = append(hunk.Find, "")
				hunk.Replace = append(hunk.Replace, "")
			} else if cur != nil && cur.Kind == PatchAdd && len(cur.Add) > 0 {
				cur.Add = append(cur.Add, "")
			}
		default:
			if hunk != nil {
				// 上下文行带一个前导空格（" one"），剥掉后匹配文件内容。
				context := strings.TrimPrefix(line, " ")
				hunk.Find = append(hunk.Find, context)
				hunk.Replace = append(hunk.Replace, context)
			} else if cur != nil && cur.Kind == PatchAdd {
				cur.Add = append(cur.Add, line)
			}
		}
	}
	if !started {
		return nil, fmt.Errorf("missing *** Begin Patch")
	}
	return ops, nil
}

// ApplyHunks applies update hunks to a file's content, requiring each
// hunk's Find sequence to match exactly once.
func ApplyHunks(content string, hunks []PatchHunk) (string, int, error) {
	lines := strings.Split(content, "\n")
	applied := 0
	for _, hunk := range hunks {
		if len(hunk.Find) == 0 {
			return content, applied, fmt.Errorf("empty hunk")
		}
		at, count := findSubseq(lines, hunk.Find)
		switch count {
		case 0:
			return content, applied, fmt.Errorf(
				"hunk context not found; read the file and copy it exactly")
		case 1:
			lines = append(lines[:at], append(append([]string(nil), hunk.Replace...), lines[at+len(hunk.Find):]...)...)
			applied++
		default:
			return content, applied, fmt.Errorf("hunk matched %d places; add context lines to make it unique", count)
		}
	}
	return strings.Join(lines, "\n"), applied, nil
}

// findSubseq finds the line index where seq occurs, and how many times.
func findSubseq(lines, seq []string) (int, int) {
	at, count := -1, 0
	for i := 0; i+len(seq) <= len(lines); i++ {
		match := true
		for j := range seq {
			if lines[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			if count == 0 {
				at = i
			}
			count++
		}
	}
	return at, count
}
