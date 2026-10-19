// Package tooldefs 发现用户自定义工具（.zgo/tools/*.md，spec 规则 75）
// ——commands/skills/agents 家族第四员：把固定命令流程包装成模型可调
// 用的工具（模板 + $ARGUMENTS 透传），省去模型拼命令的出错面。定
// 义是 frontmatter（name/description）+ 正文命令模板；发现两层平铺
// （工作区覆盖全局，与家族同约定），工作区层过信任门（工具模板是
// 注入到执行的面——未授信仓库不得自带）。
package tooldefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 上限口径与家族对齐：描述校验 1K、模板 16KB（模板是命令不是文档）。
const (
	maxDescriptionChars = 1024
	maxTemplateBytes    = 16 << 10
	discoverReadCap     = 64 << 10
)

// Scope 标记定义根的层级。
type Scope string

const (
	ScopeProject Scope = "project" // 工作区 .zgo/tools（远程经网线）
	ScopeUser    Scope = "user"    // 全局 ~/.zgo/tools（本地）
)

// DirEntry/FS 是目录访问的最小面（本地 os 与远端适配同构，家族同型）。
type DirEntry struct {
	Name  string
	IsDir bool
}

type FS interface {
	Join(parts ...string) string
	ReadFile(name string) ([]byte, error)
	ReadDir(dir string) ([]DirEntry, error)
}

// LocalFS 用 os 实现本地根。
type LocalFS struct{}

func (LocalFS) Join(parts ...string) string { return filepath.Join(parts...) }

func (LocalFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func (LocalFS) ReadDir(dir string) ([]DirEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, len(des))
	for i, de := range des {
		out[i] = DirEntry{Name: de.Name(), IsDir: de.IsDir()}
	}
	return out, nil
}

// Root 是一层定义根。
type Root struct {
	Base  string
	Scope Scope
	FS    FS
}

func (r Root) join(rel ...string) string { return r.FS.Join(append([]string{r.Base}, rel...)...) }

// Def 是一个已发现的工具定义。
type Def struct {
	Name        string
	Description string
	Command     string // 命令模板（$ARGUMENTS 占位）
	Path        string // 可读路径形态（展示/诊断）
	Scope       Scope
}

// Diagnostic/Outcome 同家族形态。
type Diagnostic struct {
	Path    string
	Message string
}

type Outcome struct {
	Defs        []Def // 按名排序，稳定
	Diagnostics []Diagnostic
}

// Discover 扫描各根的平铺 *.md（目录项按名排序保证确定性）。同名先
// 到先得——roots 顺序即优先级（工作区在前）。
func Discover(roots []Root) Outcome {
	var out Outcome
	selected := map[string]Def{}
	for _, root := range roots {
		discoverRoot(root, selected, &out)
	}
	names := make([]string, 0, len(selected))
	for n := range selected {
		names = append(names, n)
	}
	sort.Strings(names)
	out.Defs = make([]Def, 0, len(names))
	for _, n := range names {
		out.Defs = append(out.Defs, selected[n])
	}
	return out
}

func discoverRoot(root Root, selected map[string]Def, out *Outcome) {
	entries, err := root.FS.ReadDir(root.join())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{
				Path: slash(root.Base), Message: "scanning tools root: " + err.Error()})
		}
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Name, ".md") {
			continue
		}
		file := root.join(e.Name)
		data, err := root.FS.ReadFile(file)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				out.Diagnostics = append(out.Diagnostics, Diagnostic{
					Path: slash(file), Message: "reading tool def: " + err.Error()})
			}
			continue
		}
		if len(data) > discoverReadCap {
			data = data[:discoverReadCap]
		}
		def, ok := parseDef(data, slash(file), &out.Diagnostics)
		if !ok {
			continue // parseDef 已记诊断
		}
		def.Scope = root.Scope
		if _, dup := selected[def.Name]; dup {
			continue
		}
		selected[def.Name] = def
	}
}

// parseDef 解析一个定义文件：frontmatter 提供 name/description（必
// 填），正文即命令模板（必填非空）。校验失败记诊断并丢弃。
// utf8BOM 是文件头可能携带的 BOM（容忍并剥离）。
const utf8BOM = "\uFEFF"

func parseDef(data []byte, file string, diag *[]Diagnostic) (Def, bool) {
	bad := func(msg string) (Def, bool) {
		*diag = append(*diag, Diagnostic{Path: file, Message: msg})
		return Def{Path: file}, false
	}
	text := strings.TrimPrefix(string(data), utf8BOM)
	frontmatter, hasFM := extractFrontmatter(text)
	var name, description string
	if hasFM {
		values := parseFlatYAML(frontmatter)
		name = strings.TrimSpace(values["name"])
		description = values["description"]
		if name == "" {
			return bad("tool def frontmatter must include a name")
		}
		if strings.TrimSpace(description) == "" {
			return bad("tool def frontmatter must include a description")
		}
	}
	command := strings.TrimSpace(stripFrontmatter(text))
	if name == "" || description == "" {
		return bad("tool def needs frontmatter (name/description)")
	}
	if command == "" {
		return bad("tool def body (command template) is empty")
	}
	if len(description) > maxDescriptionChars {
		description = description[:maxDescriptionChars]
	}
	if len(command) > maxTemplateBytes {
		command = command[:maxTemplateBytes]
	}
	return Def{Name: name, Description: strings.TrimSpace(description),
		Command: command, Path: file}, true
}

// extractFrontmatter/stripFrontmatter/parseFlatYAML 与技能/代理家族同
// 形态（平铺 key: value 围栏解析）。
func extractFrontmatter(text string) (string, bool) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", false
	}
	rest := text[3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return "", false
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func stripFrontmatter(text string) string {
	fm, ok := extractFrontmatter(text)
	if !ok {
		return text
	}
	idx := strings.Index(text, fm)
	if idx < 0 {
		return text
	}
	rest := text[idx+len(fm):]
	rest = strings.TrimPrefix(rest, "\n---")
	rest = strings.TrimPrefix(rest, "\r\n---")
	return strings.TrimLeft(rest, "\r\n")
}

func parseFlatYAML(fm string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimRight(line, "\r")
		if k, v, ok := strings.Cut(line, ":"); ok {
			values[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return values
}

func slash(p string) string { return filepath.ToSlash(p) }
