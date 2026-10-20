// Package tools implements the built-in agent tools: read, write, edit,
// bash, grep, glob and ls. Every file access goes through Workspace's path
// jail; every output is capped.
package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Workspace is the file access boundary of a session: all tool paths are
// resolved against Root and must stay inside it. A non-nil Remote
// delegates file ops and command runs to the SSH workspace (ssh://
// targets); the lexical jail still applies to the remote path space.
type Workspace struct {
	Root   string // absolute, cleaned (remote: the host-side root)
	Remote RemoteFS
	// Extra 是附加工作目录（spec 规则 128，/add-dir 的执行面）：额外
	// 的绝对根，jail 判定 = Root ∪ Extra（各自独立做 symlink 复查）。
	// 相对路径仍只落 Root；远程工作区 v1 不支持（Remote 单根）。
	Extra []string
}

// IsRemote reports whether ops delegate to an SSH workspace.
func (w Workspace) IsRemote() bool { return w.Remote != nil }

// rootFor 返回词法包含 abs 的根（Root 优先，其次 Extra 按序）；都不
// 包含返回空串。远程工作区只认 Root（附加目录是本地会话形态）。
func (w Workspace) rootFor(abs string) string {
	roots := []string{w.Root}
	if !w.IsRemote() {
		roots = append(roots, w.Extra...)
	}
	for _, root := range roots {
		if containsPath(root, abs) {
			return root
		}
	}
	return ""
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
	root := w.rootFor(abs)
	if root == "" {
		return "", fmt.Errorf("path %q is outside the workspace", name)
	}
	// 远程工作区走独立的真实路径复查（issue #89）：本地 resolveReal 对
	// 远程路径无意义——远程 Root 在本地不存在时静默回退纯词法（远程侧
	// 指向 jail 外的符号链接即可穿透），本地恰有同名目录时还会被本地
	// 链接污染评估结果。
	if w.IsRemote() {
		return w.resolveRemoteChecked(abs)
	}
	// 所属根不存在时保持原有纯词法行为（新建 workspace 等场景）。
	realRoot, rerr := resolveReal(root)
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

// resolveRemoteChecked 在远程路径空间做 symlink 复查（issue #89：
// #4 的远程等价物）。词法检查已由调用方完成；这里把目标转成根相对
// 路径，经 RemoteFS.ReadLink 逐组件解析——链接展开后任何形式爬出根
// （.. 越顶、根外绝对目标）即拒绝。返回词法 abs，远程侧打开时自然
// 跟随链接；TOCTOU 窗口与本地修复同级。
func (w Workspace) resolveRemoteChecked(abs string) (string, error) {
	rel := remoteRel(w, abs)
	if _, err := resolveRemoteRel(w.Remote, rel); err != nil {
		return "", fmt.Errorf("path %q is outside the workspace (remote symlink escape): %v", abs, err)
	}
	return abs, nil
}

// resolveRemoteRel 在根相对空间解析真实路径：相对链接目标按链接父
// 目录展开，绝对目标经 Client.ReadLink 归一——仍在根内的剥前缀，
// 根外的保持绝对形态（即越狱信号）。最多 40 层防循环。
func resolveRemoteRel(remote RemoteFS, rel string) (string, error) {
	const maxLinks = 40
	links := 0
	comps := splitSlashPath(rel)
	var out []string
	for i := 0; i < len(comps); i++ {
		comp := comps[i]
		switch {
		case comp == "" || comp == ".":
			continue
		case comp == "..":
			if len(out) == 0 {
				return "", fmt.Errorf("escapes the root")
			}
			out = out[:len(out)-1]
		default:
			next := strings.Join(append(append([]string{}, out...), comp), "/")
			target, isLink, err := remote.ReadLink(next)
			if err != nil || !isLink {
				out = append(out, comp)
				continue
			}
			links++
			if links > maxLinks {
				return "", fmt.Errorf("too many levels of symbolic links")
			}
			// 绝对目标（归一后仍在根外）：POSIX 以 / 开头，Windows 宿主
			// 的盘符路径（C:\…）两种形态都视为越狱。
			if strings.HasPrefix(target, "/") || windowsAbsPath(target) {
				return "", fmt.Errorf("symlink targets outside the root")
			}
			// 相对目标按链接父目录（out）展开，重放整条链。
			rest := comps[i+1:]
			comps = append(append(append([]string{}, out...), splitSlashPath(target)...), rest...)
			i = -1
			out = nil
		}
	}
	return strings.Join(out, "/"), nil
}

// windowsAbsPath 报告 s 是否形如 Windows 盘符绝对路径（C:\ 或 C:/）。
func windowsAbsPath(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if c < 'A' || c > 'z' || (c > 'Z' && c < 'a') {
		return false
	}
	return s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// splitSlashPath 把斜杠路径拆成非空组件。
func splitSlashPath(p string) []string {
	var out []string
	for _, c := range strings.Split(strings.Trim(p, "/"), "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
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

// RemoteFS is the slice of remote.Client the workspace delegates to when
// the session's target is ssh:// (the tools never dial themselves).
type RemoteFS interface {
	Exec(command string) (ExecOutcome, error)
	// StartBackground 启动不等待完成的远端命令（后台 shell 的远程形
	// 态）；返回的进程与本地后台同构（tools.BgProcess）。
	StartBackground(command string) (BgProcess, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadDir(name string) ([]DirEntry, error)
	Stat(name string) (exists, isDir bool, err error)
	Glob(pattern string) ([]string, error)
	// ReadLink 报告远程符号链接的目标（远程 jail 的 symlink 复查，
	// issue #89）；非链接时 isLink 为 false。
	ReadLink(name string) (target string, isLink bool, err error)
}

// BgProcess 是后台命令的进程抽象：本地 exec 与远端 SSH 通道同构
// （remote.BgProcess 结构化实现本接口，不反向依赖）。Stdout 的合并输
// 出流由调用方排空；Wait 返回后流到达 EOF。
type BgProcess interface {
	Start() error
	KillTree() error
	Wait() error
	Stdout() io.ReadCloser
}

// ExecOutcome mirrors remote.ExecResult without the import.
type ExecOutcome struct {
	Output   string
	ExitCode int
}

// DirEntry mirrors remote.Entry.
type DirEntry struct {
	Name  string
	IsDir bool
}
