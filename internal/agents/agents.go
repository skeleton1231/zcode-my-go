// Package agents discovers user-defined subagent personas (issue #225：
// Zgo 形态的 .zgo/agents 定义层)。两层平铺 markdown——工作区
// .zgo/agents/*.md（远程经网线）+ 用户家 ~/.zgo/agents/*.md——同名工作
// 区优先（与技能/自定义命令同约定）。frontmatter 提供 name（缺省文件
// 名主干）、description（主模型路由依据）、tools（白名单，逗号分隔）；
// 正文即该子代理的系统提示。发现是纯读扫描，SSH 工作区经 FS 适配。
package agents

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// 约束口径对齐注入家族（skills）：描述校验 1K、清单行展示截断 250、
// 注入预算 20K 字符、persona 载入上限 100KB（超限截断并标注）。发现
// 读上限须高于 persona 上限，超限判定才在发现时可见（skills 只解析
// 元数据故 64KB 足够；agents 连正文一并载入）。
const (
	maxDescriptionChars = 1024
	displayDescChars    = 250
	DefaultMetaBudget   = 20_000
	DefaultMaxBytes     = 100_000
	discoverReadCap     = DefaultMaxBytes + 1024
)

// Scope 标记定义根的层级。
type Scope string

const (
	ScopeProject Scope = "project" // 工作区 .zgo/agents（远程经网线）
	ScopeUser    Scope = "user"    // 全局 ~/.zgo/agents（本地）
)

// DirEntry 是目录项的最小面（本地 os 与远端适配同构）。
type DirEntry struct {
	Name  string
	IsDir bool
}

// FS 是一个定义根的文件访问面（与 skills.FS 同构）。
type FS interface {
	Join(parts ...string) string
	ReadFile(name string) ([]byte, error)
	ReadDir(dir string) ([]DirEntry, error)
}

// LocalFS 用 os 实现本地根。
type LocalFS struct{}

// Join implements FS.
func (LocalFS) Join(parts ...string) string { return filepath.Join(parts...) }

// ReadFile implements FS.
func (LocalFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// ReadDir implements FS.
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

// Root 是一层定义根：Base 参与路径拼接与展示。
type Root struct {
	Base  string
	Scope Scope
	FS    FS
}

func (r Root) join(rel ...string) string { return r.FS.Join(append([]string{r.Base}, rel...)...) }

// Def 是一个已发现的子代理定义。
type Def struct {
	Name        string
	Description string
	Tools       []string // 白名单（空即只读族，对齐现状 SubtaskSystem）
	System      string   // persona（正文）
	Path        string   // 可读路径形态（展示/诊断用）
	Scope       Scope
	Truncated   bool // persona 超 100KB 被截断
}

// Diagnostic 是发现过程中的非致命问题（坏定义跳过，不拖垮整层）。
type Diagnostic struct {
	Path    string
	Message string
}

// Outcome 是一次发现的结果。
type Outcome struct {
	Defs        []Def // 按名排序，稳定
	Diagnostics []Diagnostic
}

// utf8BOM 是文件头可能携带的 BOM（容忍并剥离）。
const utf8BOM = "\\uFEFF"

// Discover 扫描各根的平铺 *.md（目录项按名排序保证确定性；读失败记
// 诊断继续）。同名先到先得——roots 顺序即优先级（工作区在前）。
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

// discoverRoot 扫描一个根，命中并入 selected（同名不覆盖）。
func discoverRoot(root Root, selected map[string]Def, out *Outcome) {
	entries, err := root.FS.ReadDir(root.join())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{
				Path: slash(root.Base), Message: "scanning agents root: " + err.Error()})
		}
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Name, ".md") {
			continue
		}
		def, ok := parseDef(root, e.Name, &out.Diagnostics)
		if !ok {
			continue
		}
		if _, dup := selected[def.Name]; dup {
			continue
		}
		selected[def.Name] = def
	}
}

// parseDef 读取并解析一个定义文件：frontmatter（--- 围住的平铺
// key: value）提供 name/description/tools，正文为 persona。校验失败
// 记诊断并丢弃——description 是路由依据必填，persona 为空的定义没有
// 使用价值，同样丢弃。
func parseDef(root Root, file string, diag *[]Diagnostic) (Def, bool) {
	path := root.join(file)
	data, err := root.FS.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			*diag = append(*diag, Diagnostic{Path: slash(path),
				Message: "reading agent: " + err.Error()})
		}
		return Def{}, false
	}
	if len(data) > discoverReadCap {
		data = data[:discoverReadCap]
	}
	d := Def{Path: slash(path), Scope: root.Scope}
	text := strings.TrimPrefix(string(data), utf8BOM)
	frontmatter, hasFM := extractFrontmatter(text)
	name, description, tools := "", "", ""
	if hasFM {
		values := parseFlatYAML(frontmatter)
		name = strings.TrimSpace(values["name"])
		description = values["description"]
		tools = values["tools"]
	}
	if name == "" {
		// 缺省取文件名主干（去 .md）。
		name = strings.TrimSuffix(file, ".md")
	}
	if !validName(name) {
		*diag = append(*diag, Diagnostic{Path: d.Path,
			Message: "invalid agent name " + strconv.Quote(name)})
		return Def{}, false
	}
	if strings.TrimSpace(description) == "" {
		*diag = append(*diag, Diagnostic{Path: d.Path,
			Message: "agent " + name + " needs a description (routing key)"})
		return Def{}, false
	}
	if len([]rune(description)) > maxDescriptionChars {
		*diag = append(*diag, Diagnostic{Path: d.Path,
			Message: "agent description is too long: " + name})
		return Def{}, false
	}
	system := strings.TrimSpace(stripFrontmatter(text))
	if system == "" {
		*diag = append(*diag, Diagnostic{Path: d.Path,
			Message: "agent " + name + " has no persona body"})
		return Def{}, false
	}
	if len(system) > DefaultMaxBytes {
		system = system[:DefaultMaxBytes] + "\n\n(agent definition truncated)"
		d.Truncated = true
	}
	d.Name, d.Description, d.Tools, d.System = name, strings.TrimSpace(description), parseTools(tools), system
	return d, true
}

// parseTools 解析逗号/空白混合分隔的白名单：去空、去重、保序。
func parseTools(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// validName 收敛名字：非空、无路径分隔符、无空白。
func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) &&
		!strings.ContainsFunc(name, unicode.IsSpace)
}

// extractFrontmatter 抽出 --- 围住的头部。
func extractFrontmatter(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", false
}

// stripFrontmatter 去掉头部，返回正文。
func stripFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return content
}

// parseFlatYAML 解析平铺 key: value 行（受控子集，不引第三方依赖）。
func parseFlatYAML(text string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' ||
			val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		values[key] = val
	}
	return values
}

// Find 在发现结果中按名精确匹配。
func Find(out Outcome, name string) (Def, bool) {
	for _, d := range out.Defs {
		if d.Name == name {
			return d, true
		}
	}
	return Def{}, false
}

// Names 列出全部定义名（未命中报错自纠用）。
func Names(out Outcome) []string {
	names := make([]string, 0, len(out.Defs))
	for _, d := range out.Defs {
		names = append(names, d.Name)
	}
	return names
}

// Section 渲染系统提示注入块（"" 当无定义）：完整形态一行一个
// `名: 描述`；超预算降级为仅名列（渐进披露）。
func Section(out Outcome, budget int) string {
	if len(out.Defs) == 0 {
		return ""
	}
	if budget <= 0 {
		budget = DefaultMetaBudget
	}
	lines := make([]string, 0, len(out.Defs))
	for _, d := range out.Defs {
		desc := d.Description
		if runes := []rune(desc); len(runes) > displayDescChars {
			desc = string(runes[:displayDescChars-1]) + "…"
		}
		lines = append(lines, "- "+d.Name+": "+desc)
	}
	header := "The following named subagents are available " +
		"(pass \"agent\": \"<name>\" to task/dispatch to use one):\n"
	full := header + strings.Join(lines, "\n")
	if len(full) <= budget {
		return "\n" + full + "\n"
	}
	names := make([]string, 0, len(out.Defs))
	for _, d := range out.Defs {
		names = append(names, "- "+d.Name)
	}
	return "\n" + header + strings.Join(names, "\n") + "\n"
}

// slash 归一为斜杠形态（跨平台测试断言不因分隔符漂移）。
func slash(p string) string { return filepath.ToSlash(path.Clean(p)) }
