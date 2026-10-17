// Package tools implements the built-in agent tools: read, write, edit,
// bash, grep, glob and ls. Every file access goes through Workspace's path
// jail; every output is capped.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Workspace is the file access boundary of a session: all tool paths are
// resolved against Root and must stay inside it.
type Workspace struct {
	Root string // absolute, cleaned
}

// Resolve turns a tool path argument into an absolute path inside the
// workspace. Relative paths join Root; absolute paths must already be
// inside. Anything escaping Root is an error, never a redirect. The returned
// path is the fully resolved real path（符号链接/junction 已展开）.
//
// 词法检查挡不住指向 jail 外的符号链接/junction（issue #4，Windows 上
// mklink /J 无需特权即可创建）。词法通过后必须再做真实路径复查：把目标
// 的真实路径解析出来（write 新建目标解析最深已存在祖先），确认仍在 Root
// 内。stdlib 的两个现成原语都不合用：filepath.EvalSymlinks 在 Windows
// 实测不展开 junction（Go 1.27 的 Lstat 把 junction 报成 ModeIrregular
// 的普通项）；os.Root 在 Windows 上则拒绝一切 junction，指向 jail 内部的
// 合法链接（monorepo 场景）会被误伤。因此手写逐组件解析（Readlink 对
// symlink 与 junction 都能读出目标）。
func (w Workspace) Resolve(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("empty path")
	}
	abs := name
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.Root, name)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(w.Root, abs)
	if err != nil || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", name)
	}
	// Root 不存在时保持原有纯词法行为（新建 workspace 等场景）。
	realRoot, rerr := resolveReal(w.Root)
	if rerr != nil {
		return abs, nil
	}
	// write 的目标可能尚不存在：上溯到最深已存在祖先再解析。
	existing := abs
	for {
		if _, serr := os.Stat(existing); serr == nil {
			break
		} else if !os.IsNotExist(serr) {
			return "", fmt.Errorf("path %q: %v", name, serr)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			// Root 存在时不可能走到这里（上溯必停在 Root），防御性兜底。
			return abs, nil
		}
		existing = parent
	}
	real, rerr := resolveReal(existing)
	if rerr != nil {
		return "", fmt.Errorf("path %q: %v", name, rerr)
	}
	if !containsPath(realRoot, real) {
		return "", fmt.Errorf("path %q is outside the workspace (symlink escape)", name)
	}
	rest := strings.TrimPrefix(abs[len(existing):], string(filepath.Separator))
	return filepath.Join(real, rest), nil
}

// containsPath 报告 abs 是否在 root 内（含等于 root）。
func containsPath(root, abs string) bool {
	if rel, err := filepath.Rel(root, abs); err == nil &&
		rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	// Windows 大小写不敏感：调用方传入的 Root 与磁盘实际大小写可能不同
	// （如小写盘符），不能因此误拒。
	if runtime.GOOS == "windows" {
		r := strings.ToLower(strings.TrimSuffix(filepath.Clean(root), string(filepath.Separator)))
		a := strings.ToLower(filepath.Clean(abs))
		return a == r || strings.HasPrefix(a, r+string(filepath.Separator))
	}
	return false
}

// resolveReal 逐组件解析 p 的真实路径：符号链接与 junction 都跟随。
// links 计数防循环引用。
func resolveReal(p string) (string, error) {
	const maxLinks = 40
	links := 0
	cur := filepath.Clean(p)
	for restart := true; restart; {
		restart = false
		vol := filepath.VolumeName(cur)
		comps := splitPath(strings.TrimPrefix(cur, vol))
		// VolumeName 返回 "C:"（无尾分隔符）：必须补上，否则 Join("C:", x)
		// 会被当作驱动器相对路径拼出 "C:x\..."。
		resolved := vol + string(filepath.Separator)
		for i := 0; i < len(comps); i++ {
			if comps[i] == ".." {
				resolved = filepath.Dir(resolved)
				continue
			}
			next := filepath.Join(resolved, comps[i])
			st, err := os.Lstat(next)
			if err != nil {
				return "", err
			}
			// ModeIrregular 在 Windows 上即 junction 等 reparse point。
			if st.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				target, err := os.Readlink(next)
				if err != nil {
					return "", err
				}
				links++
				if links > maxLinks {
					return "", fmt.Errorf("too many levels of symbolic links: %s", p)
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(next), target)
				}
				tvol := filepath.VolumeName(target)
				tcomps := splitPath(strings.TrimPrefix(target, tvol))
				cur = filepath.Join(tvol+string(filepath.Separator),
					strings.Join(append(tcomps, comps[i+1:]...), string(filepath.Separator)))
				restart = true
				break
			}
			resolved = next
		}
		if !restart {
			return resolved, nil
		}
	}
	return cur, nil
}

// splitPath 把绝对路径去掉卷名后的部分拆成非空组件（分隔符与卷名
// 前缀都被吸收掉）。
func splitPath(p string) []string {
	trimmed := strings.Trim(strings.TrimPrefix(p, string(filepath.Separator)),
		string(filepath.Separator))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, string(filepath.Separator))
}
