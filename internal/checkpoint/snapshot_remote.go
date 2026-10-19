package checkpoint

// 远程工作区的文件快照（spec 规则 84）：ssh:// 会话此前完全降级（连
// 「仅回退转录」的文件半边都没有）——这里把快照库的捕获/恢复抽象到
// FS 面（本地 os 与 tools.RemoteFS 同构），远程整树拉回本地快照库，
// 恢复时镜像推回（WriteFile 覆写 + Exec rm 删快照后新增）。
//
// 远程路径一律根相对斜杠形态（RemoteFS 的约定）；上限/保留窗口/错
// 误分层与本地快照同源（复用 snapRoot 与哨兵）。

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// RemoteFS 是快照所需的最小远程面（tools.RemoteFS 的窄化——Exec 只
// 用于删除，rm 单文件/空目录）。
type remoteFS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadDir(name string) ([]tools.DirEntry, error)
	Exec(command string) (tools.ExecOutcome, error)
}

// SnapshotCaptureRemote 把远程工作区整树拉进本地快照库。
func SnapshotCaptureRemote(dataDir string, r remoteFS, session string, seq int) error {
	dst := filepathJoin(dataDir, "snapshots", session, strconv.Itoa(seq))
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if err := pullTree(r, "", dst); err != nil {
		os.RemoveAll(snapRootLocal(dataDir, session))
		return err
	}
	return SnapshotPrune(dataDir, session, Retention)
}

// snapRootLocal 是 snapRoot 的本文件内别名（远程文件避免 import 环
// 之外的重复定义——直接复用 snapshot.go 的 snapRoot）。
func snapRootLocal(dataDir, session string) string { return snapRoot(dataDir, session) }

// pullTree 递归拉取远程子树（rel 根相对；"" = 根）。上限同本地。
func pullTree(r remoteFS, rel string, dst string) error {
	entries, err := r.ReadDir(rel)
	if err != nil {
		return err
	}
	files, total := 0, int64(0)
	for _, e := range entries {
		name := e.Name
		if name == ".DS_Store" || name == "Thumbs.db" {
			continue
		}
		child := path.Join(rel, name)
		if e.IsDir {
			if name == ".git" && child == ".git" {
				continue
			}
			if err := pullTreeCounted(r, child, filepathJoin(dst, name), &files, &total); err != nil {
				return err
			}
			continue
		}
		data, err := r.ReadFile(child)
		if err != nil {
			continue // 单文件读失败（权限/瞬时）：跳过不拖垮整树
		}
		files++
		if files > SnapMaxFiles {
			return ErrTooLarge
		}
		total += int64(len(data))
		if total > SnapMaxBytes {
			return ErrTooLarge
		}
		target := filepathJoin(dst, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// pullTreeCounted 带跨目录计数器的递归体。
func pullTreeCounted(r remoteFS, rel, dst string, files *int, total *int64) error {
	entries, err := r.ReadDir(rel)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name
		if name == ".DS_Store" || name == "Thumbs.db" {
			continue
		}
		child := path.Join(rel, name)
		if e.IsDir {
			if name == ".git" && child == ".git" {
				continue
			}
			if err := pullTreeCounted(r, child, filepathJoin(dst, name), files, total); err != nil {
				return err
			}
			continue
		}
		data, err := r.ReadFile(child)
		if err != nil {
			continue
		}
		*files++
		if *files > SnapMaxFiles {
			return ErrTooLarge
		}
		*total += int64(len(data))
		if *total > SnapMaxBytes {
			return ErrTooLarge
		}
		target := filepathJoin(dst, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotRestoreRemote 把本地快照镜像推回远程：快照内覆写、快照后
// 新增删除（Exec rm）。错误分层与本地同源。
func SnapshotRestoreRemote(dataDir string, r remoteFS, session string, seq int) error {
	root := snapRoot(dataDir, session)
	ents, err := os.ReadDir(root)
	if err != nil || len(ents) == 0 {
		return ErrSnapshotMissing
	}
	src := filepathJoin(root, strconv.Itoa(seq))
	if _, err := os.Stat(src); err != nil {
		return ErrSnapshotPruned
	}
	// 删远程多余（快照里没有的）。快照相对路径集合。
	inSnap := map[string]bool{}
	err = walkLocalRel(src, "", func(rel string) bool {
		inSnap[normalizeSlash(rel)] = true
		return false
	})
	if err != nil {
		return err
	}
	var extras []string
	err = walkRemote(r, "", func(rel string) {
		if !inSnap[rel] {
			extras = append(extras, rel)
		}
	})
	if err != nil {
		return err
	}
	// 删除顺序：先深（子文件先删）后浅；rm -f 单文件。
	sort.Sort(sort.Reverse(sort.StringSlice(extras)))
	for _, rel := range extras {
		if _, err := r.Exec("rm -f " + quoteRemote(rel)); err != nil {
			return err
		}
	}
	// 快照全量推回。
	return pushTree(r, src, "")
}

// pushTree 把本地快照子树推回远程（rel 是远程相对路径前缀）。
func pushTree(r remoteFS, src, rel string) error {
	ents, err := os.ReadDir(filepathJoin(src, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	for _, e := range ents {
		child := path.Join(rel, e.Name())
		if e.IsDir() {
			if err := pushTree(r, src, child); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(filepathJoin(src, filepath.FromSlash(child)))
		if err != nil {
			return err
		}
		if err := r.WriteFile(child, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// walkRemote 遍历远程工作区，对每个文件回调（相对斜杠路径）。
func walkRemote(r remoteFS, rel string, fn func(rel string)) error {
	entries, err := r.ReadDir(rel)
	if err != nil {
		return err
	}
	for _, e := range entries {
		child := path.Join(rel, e.Name)
		if e.IsDir {
			if e.Name == ".git" && child == ".git" {
				continue
			}
			if err := walkRemote(r, child, fn); err != nil {
				return err
			}
			continue
		}
		fn(child)
	}
	return nil
}

// walkLocalRel 遍历本地快照目录，对每个文件回调（相对路径）。
func walkLocalRel(root, rel string, fn func(rel string) bool) error {
	ents, err := os.ReadDir(filepathJoin(root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	for _, e := range ents {
		child := path.Join(rel, e.Name())
		if e.IsDir() {
			if err := walkLocalRel(root, child, fn); err != nil {
				return err
			}
			continue
		}
		if fn(child) {
			return nil
		}
	}
	return nil
}

// quoteRemote 给远程路径加单引号（rm 参数注入面——路径字符集来自
// ReadDir，含空格等；单引号内除单引号外无展开）。
func quoteRemote(rel string) string {
	return "'" + strings.ReplaceAll(rel, "'", `'\''`) + "'"
}

// normalize 把本地分隔符相对路径归一为斜杠。
func normalizeSlash(rel string) string { return filepath.ToSlash(rel) }

// filepathJoin 是 path/filepath.Join 的别名锚（本地侧统一走标准库）。
func filepathJoin(parts ...string) string { return filepath.Join(parts...) }
