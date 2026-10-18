// Package skills discovers and loads reusable local instruction packs
// (ZCode 的 skill 子系统：SKILL.md + frontmatter 元数据、按名加载、
// 系统提示注入可用清单)。发现是纯读扫描：工作区 .zgo/skills 与全局
// ~/.zgo/skills 两层（同名工作区覆盖全局——与自定义命令同约定），
// SSH 工作区经 FS 适配走网线。
package skills

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

// skillFile 是技能清单文件名（技能目录内）。
const skillFile = "SKILL.md"

// 常量口径对齐 ZCode：frontmatter 描述校验 1K、清单行展示截断 250、
// 注入预算 20K 字符、加载上限 100KB（超限截断并标注）、发现读上限
// 64KB（元数据解析只看头部，防病态大文件拖慢每次发现）。
const (
	maxDescriptionChars  = 1024
	displayDescChars     = 250
	DefaultMetaBudget    = 20_000
	DefaultMaxSkillBytes = 100_000
	discoverReadCap      = 64_000
)

// Scope 标记技能根的层级。
type Scope string

const (
	ScopeProject Scope = "project" // 工作区 .zgo/skills（远程经网线）
	ScopeUser    Scope = "user"    // 全局 ~/.zgo/skills（本地）
)

// DirEntry 是目录项的最小面（本地 os 与远端适配同构）。
type DirEntry struct {
	Name  string
	IsDir bool
}

// FS 是一个技能根的文件访问面。路径由 Join 产生：本地根拼绝对路径
// （平台分隔符），远端根保持根相对斜杠形态——两类根对 Discover 同构。
type FS interface {
	Join(parts ...string) string
	ReadFile(name string) ([]byte, error)
	ReadDir(dir string) ([]DirEntry, error)
}

// LocalFS 用 os 实现本地根（Join 得到绝对路径）。
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

// Root 是一层技能根：Base 参与路径拼接与展示（本地绝对 / 远端根相对）。
type Root struct {
	Base  string
	Scope Scope
	FS    FS
}

func (r Root) join(rel ...string) string { return r.FS.Join(append([]string{r.Base}, rel...)...) }

// utf8BOM 是文件头可能携带的 BOM（容忍并剥离）。
const utf8BOM = "\uFEFF"

// Skill 是一个已发现的技能的元数据。
type Skill struct {
	Name        string
	Description string
	WhenToUse   string
	// Path 是 SKILL.md 的完整可读路径形态（本地绝对 / 远端根相对，
	// 斜杠形态）——展示给模型，read 工具可直接读（本地需在 jail 内）。
	Path  string
	Scope Scope
}

// Diagnostic 是发现过程中的非致命问题（坏技能跳过，不拖垮整层）。
type Diagnostic struct {
	Path    string
	Message string
}

// Outcome 是一次发现的结果。
type Outcome struct {
	Skills      []Skill // 按名排序，稳定
	Diagnostics []Diagnostic
}

// Discover 扫描各根：根自身 SKILL.md（单技能根形态）+ 一层子目录的
// <名>/SKILL.md。目录项按名排序保证确定性；读失败（非不存在）记诊断
// 继续。同名技能先到先得——roots 顺序即优先级（工作区在前）。
func Discover(roots []Root) Outcome {
	var out Outcome
	selected := map[string]Skill{}
	for _, root := range roots {
		discoverRoot(root, selected, &out)
	}
	names := make([]string, 0, len(selected))
	for n := range selected {
		names = append(names, n)
	}
	sort.Strings(names)
	out.Skills = make([]Skill, 0, len(names))
	for _, n := range names {
		out.Skills = append(out.Skills, selected[n])
	}
	return out
}

// discoverRoot 扫描一个根，命中并入 selected（同名不覆盖——先到先得）。
func discoverRoot(root Root, selected map[string]Skill, out *Outcome) {
	add := func(rel string, nameDefault string) {
		file := root.join(rel)
		data, err := root.FS.ReadFile(file)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				out.Diagnostics = append(out.Diagnostics, Diagnostic{
					Path: slash(file), Message: "reading skill: " + err.Error()})
			}
			return
		}
		if len(data) > discoverReadCap {
			data = data[:discoverReadCap]
		}
		skill, ok := parseSkill(data, slash(file), nameDefault, &out.Diagnostics)
		if !ok {
			return
		}
		if _, dup := selected[skill.Name]; dup {
			return
		}
		selected[skill.Name] = skill
	}

	// 根自身的 SKILL.md：单技能根形态（manifest 可指向单技能）。
	add(skillFile, baseName(root.Base))

	entries, err := root.FS.ReadDir(root.join())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{
				Path: slash(root.Base), Message: "scanning skill root: " + err.Error()})
		}
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		add(path.Join(e.Name, skillFile), e.Name)
	}
}

// parseSkill 解析一个 SKILL.md：frontmatter（--- 围住的平铺 key: value）
// 提供 name/description/when_to_use；无 frontmatter 时按目录名收录
// （手写技能仍可用，description 为空）。校验失败记诊断并丢弃。
func parseSkill(data []byte, file, nameDefault string, diag *[]Diagnostic) (Skill, bool) {
	s := Skill{Path: file}
	text := strings.TrimPrefix(string(data), utf8BOM)
	frontmatter, hasFM := extractFrontmatter(text)
	var name, description, whenToUse string
	if hasFM {
		values := parseFlatYAML(frontmatter)
		name = strings.TrimSpace(values["name"])
		description = values["description"]
		whenToUse = values["when_to_use"]
		if name == "" {
			*diag = append(*diag, Diagnostic{Path: file,
				Message: "skill frontmatter must include a name"})
			return Skill{}, false
		}
		if strings.TrimSpace(description) == "" {
			*diag = append(*diag, Diagnostic{Path: file,
				Message: "skill frontmatter must include a description"})
			return Skill{}, false
		}
	} else {
		name = nameDefault
	}
	if !validSkillName(name) {
		*diag = append(*diag, Diagnostic{Path: file,
			Message: "invalid skill name " + strconv.Quote(name)})
		return Skill{}, false
	}
	if len([]rune(description)) > maxDescriptionChars {
		*diag = append(*diag, Diagnostic{Path: file,
			Message: "skill description is too long: " + name})
		return Skill{}, false
	}
	s.Name, s.Description, s.WhenToUse = name, strings.TrimSpace(description), strings.TrimSpace(whenToUse)
	return s, true
}

// validSkillName 收敛技能名：非空、无路径分隔符、无空白——名字用于展示
// 与精确匹配（加载按发现到的 Path 读），保持可读可点即可。
func validSkillName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) &&
		!strings.ContainsFunc(name, unicode.IsSpace)
}

// baseName 取路径最后一段（两种分隔符都认：本地绝对与远端斜杠形态）。
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// extractFrontmatter 抽出 --- 围住的头部（含失败即无 frontmatter）。
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

// parseFlatYAML 解析平铺 key: value 行（值支持成对引号；注释与空行跳过）。
// 技能 frontmatter 是受控的平铺子集，不引第三方 YAML 依赖。
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

// ErrSkillNotFound 由 Load 返回；调用方附带可用名列表自纠。
var ErrSkillNotFound = errors.New("skill not found")

// Find 在发现结果中按名精确匹配。
func Find(out Outcome, name string) (Skill, bool) {
	for _, s := range out.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// LoadSkill 读取技能正文：去 frontmatter、TrimSpace；超 maxBytes 截断
// （截断即标注，不静默）。base 返回技能目录（技能内相对文件的解析基）。
func LoadSkill(root Root, s Skill, maxBytes int) (content, base string, truncated bool, err error) {
	// 根随发现同源：Skill.Path 由该根 Join 产生，直接可读。
	data, err := root.FS.ReadFile(s.Path)
	if err != nil {
		return "", "", false, err
	}
	if len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}
	content = strings.TrimSpace(stripFrontmatter(strings.TrimPrefix(string(data), utf8BOM)))
	base = path.Dir(s.Path)
	return content, base, truncated, nil
}

// RootOf 在 roots 里找出产出该技能的根（按 Path 前缀匹配；同名先到先
// 得后 selected 记录的即首个根）。
func RootOf(roots []Root, s Skill) (Root, bool) {
	for _, r := range roots {
		if strings.HasPrefix(s.Path, slash(r.Base)) {
			return r, true
		}
	}
	return Root{}, false
}

// Section 渲染系统提示注入块（"" 当无技能）：完整形态一行一技能
// `名: 描述 — 何时用 (file: 路径)`；超预算降级为仅名列（渐进披露——
// 名字足够让模型用 skill 工具按名加载）。
func Section(out Outcome, budget int) string {
	if len(out.Skills) == 0 {
		return ""
	}
	if budget <= 0 {
		budget = DefaultMetaBudget
	}
	lines := make([]string, 0, len(out.Skills))
	for _, s := range out.Skills {
		lines = append(lines, formatSkillLine(s))
	}
	full := "The following skills are available (the skill tool loads one by name):\n" +
		strings.Join(lines, "\n")
	if len(full) <= budget {
		return "\n" + full + "\n"
	}
	names := make([]string, 0, len(out.Skills))
	for _, s := range out.Skills {
		names = append(names, "- "+s.Name+" (file: "+s.Path+")")
	}
	return "\nThe following skills are available (the skill tool loads one by name):\n" +
		strings.Join(names, "\n") + "\n"
}

// formatSkillLine 渲染一行技能元数据（描述展示截断）。
func formatSkillLine(s Skill) string {
	desc := s.Description
	if s.WhenToUse != "" {
		desc += " — " + s.WhenToUse
	}
	if runes := []rune(desc); len(runes) > displayDescChars {
		desc = string(runes[:displayDescChars-1]) + "…"
	}
	return "- " + s.Name + ": " + desc + " (file: " + s.Path + ")"
}

// slash 归一为斜杠形态（远端与展示统一；本地绝对路径也转，跨平台测试
// 断言不因分隔符漂移）。
func slash(p string) string { return filepath.ToSlash(p) }
