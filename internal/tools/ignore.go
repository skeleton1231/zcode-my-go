package tools

// .gitignore 感知的遍历忽略表（issue #208：glob/grep 此前只有 8 个静
// 态 skipDirs——生成物先把结果预算吃光，真实文件排在截断线之后）。
// 实现 git 风格模式的最小可用子集：注释/空行、`!` 反选、尾随 `/` 目
// 录限定、含 `/` 锚定到根、`*`/`?` 通配（不跨段）；`**` 不支持（按字
// 面处理）。与静态 skipDirs 取并集（无 .gitignore 时行为不变）。

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ignorePattern 是一条解析后的规则。
type ignorePattern struct {
	negate  bool
	dirOnly bool
	anchor  bool // 含前导或内部斜杠：相对根匹配；否则按段名匹配任意层
	pat     string
}

// ignoreList 是一个工作区的忽略规则（有序，后命中者胜——git 语义）。
type ignoreList struct {
	patterns []ignorePattern
}

// loadIgnores 解析工作区根的 .gitignore（读取经 workspace 抽象——远
// 程同享，吸取 review #83 教训）。失败/缺失 → 空表（静态 skipDirs 兜
// 底）。会话级加载一次（工具随会话构造；.gitignore 变更下轮会话生
// 效——对齐 AGENTS.md 发现的缓存取舍）。
func loadIgnores(ws Workspace) *ignoreList {
	ig := &ignoreList{}
	data, err := readWorkspaceFile(ws, ".gitignore")
	if err != nil || len(data) == 0 {
		return ig
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := ignorePattern{}
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = line[1:]
		}
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			p.anchor = true
			line = strings.TrimPrefix(line, "/")
		} else if strings.Contains(line, "/") {
			p.anchor = true
		}
		p.pat = line
		ig.patterns = append(ig.patterns, p)
	}
	return ig
}

// readWorkspaceFile 经 workspace 抽象读根相对文件（本地或远程）。
func readWorkspaceFile(ws Workspace, rel string) ([]byte, error) {
	if ws.IsRemote() {
		return ws.Remote.ReadFile(rel)
	}
	return os.ReadFile(filepath.Join(ws.Root, rel))
}

// match 报告 rel（斜杠分隔、根相对）是否被忽略。isDir 用于目录限定
// 规则。后命中者胜。
func (ig *ignoreList) match(rel string, isDir bool) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." {
		return false
	}
	ignored := false
	for _, p := range ig.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.anchor {
			if ok, err := path.Match(p.pat, rel); err == nil && ok {
				ignored = !p.negate
			}
			continue
		}
		// 非锚定：任一段名命中即算（git 的任意层匹配），或整段路径
		// 逐段前缀拼合匹配（vendor 这类目录名）。
		for _, seg := range strings.Split(rel, "/") {
			if ok, err := path.Match(p.pat, seg); err == nil && ok {
				ignored = !p.negate
				break
			}
		}
	}
	return ignored
}

// empty 是否无规则（快速路径：walk 钩子零开销）。
func (ig *ignoreList) empty() bool { return len(ig.patterns) == 0 }

// relToRoot 计算根相对斜杠路径（walk 钩子用；失败返回原样，匹配自然
// 落空）。
func relToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}
