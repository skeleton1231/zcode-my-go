package checkpoint

// 文件快照存储（spec 规则 83）：非 git 工作区的检查点降级路径——git
// 临时索引方案对非仓库返回 ErrNotARepo 后此前只剩「仅回退转录」。这
// 里补另一半：把工作区整树按相对路径复制进 dataDir 的快照库
//（<dataDir>/snapshots/<session>/<seq>/），恢复时镜像回去（删除快照
// 后新增的文件——对齐 git 路径的 read-tree -u --reset + clean -fd 语
// 义）。
//
// 上限：单会话快照总量 100MB / 文件数 5000——超限返回 ErrTooLarge，
// 调用方降级回「仅回退转录」（与既往行为一致，不因大目录失败）。
// 符号链接跳过（不跟踪、恢复不重建——复制符号链接目标会意外展开）。

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// 快照上限（总量按单会话全部 seq 累计——修剪发生在 capture 尾部，超
// 限前不积累）。
const (
	SnapMaxFiles = 5000
	SnapMaxBytes = 100 << 20
)

var (
	// ErrSnapshotMissing：该会话从未有过快照（特性上线前的旧会话）
	// ——调用方降级为仅回退转录（与 ErrNotARepo 同级）。
	ErrSnapshotMissing = errors.New("no snapshots for session")
	// ErrSnapshotPruned：会话有快照但该回合的已修剪——中止回退（对
	// 齐 git 路径的 ErrCheckpointPruned 语义，issue #152）。
	ErrSnapshotPruned = errors.New("snapshot pruned (outside retention window)")
	// ErrTooLarge：超上限——调用方降级。
	ErrTooLarge = errors.New("workspace exceeds snapshot caps")
)

// snapRoot 是快照库根（dataDir 下）。
func snapRoot(dataDir, session string) string {
	return filepath.Join(dataDir, "snapshots", session)
}

// SnapshotCapture 把工作区整树复制进快照库并按保留窗口修剪。
func SnapshotCapture(dataDir, workspace, session string, seq int) error {
	dst := filepath.Join(snapRoot(dataDir, session), strconv.Itoa(seq))
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	files, bytes, err := copyTree(workspace, dst)
	if err != nil {
		// 半截快照不留（会话根一并清——空根会让 Restore 误判有快照）
		//——恢复面要么全有要么全无。
		os.RemoveAll(snapRoot(dataDir, session))
		return err
	}
	_ = files
	_ = bytes
	if err := SnapshotPrune(dataDir, session, Retention); err != nil {
		return err
	}
	return nil
}

// copyTree 递归复制 src → dst（相对路径保持）；返回（文件数, 总字节）。
// 上限命中即 ErrTooLarge（dst 由调用方清理）。
func copyTree(src, dst string) (int, int64, error) {
	files, total := 0, int64(0)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" && rel == ".git" {
				return filepath.SkipDir // 仓库内脏不入快照（快照路径是非仓库，防御性跳过）
			}
			return nil
		}
		if name == ".DS_Store" || name == "Thumbs.db" {
			return nil // 系统噪音不入库
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // 符号链接跳过
		}
		files++
		if files > SnapMaxFiles {
			return ErrTooLarge
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil // 管道/socket 等特殊文件不复制
		}
		total += fi.Size()
		if total > SnapMaxBytes {
			return ErrTooLarge
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return copyFile(p, target, fi)
	})
	return files, total, err
}

// copyFile 单文件复制（权限随源）。
func copyFile(src, dst string, fi fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// SnapshotRestore 把工作区镜像回 seq 的快照：快照内的文件覆写回去、
// 快照后新增的文件删除（对齐 git 路径 clean -fd 语义——回退是「回到
// 当时」不是「回到当时之上再叠加」）。会话无快照 → ErrSnapshotMissing
// （旧会话降级）；有快照但该 seq 无 → ErrSnapshotPruned（中止）。
func SnapshotRestore(dataDir, workspace, session string, seq int) error {
	root := snapRoot(dataDir, session)
	if ents, err := os.ReadDir(root); err != nil || len(ents) == 0 {
		return ErrSnapshotMissing
	}
	src := filepath.Join(root, strconv.Itoa(seq))
	if _, err := os.Stat(src); err != nil {
		return ErrSnapshotPruned
	}
	// 先删快照后新增的（工作区里有、快照里没有）。
	return mirrorBack(src, workspace)
}

// mirrorBack 镜像恢复：walk 工作区删多余，再复制快照全量覆写。
func mirrorBack(src, workspace string) error {
	// 第一遍：删除工作区里快照没有的文件（目录最后删空壳）。
	var dirs []string
	err := filepath.WalkDir(workspace, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(workspace, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" && rel == ".git" {
				return filepath.SkipDir
			}
			dirs = append(dirs, rel)
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if _, err := os.Stat(filepath.Join(src, rel)); err != nil {
			return os.Remove(p) // 快照里没有：快照后新增——删
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 空目录自底向上清（快照里没有的目录壳）。
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, rel := range dirs {
		if _, err := os.Stat(filepath.Join(src, rel)); err != nil {
			os.Remove(filepath.Join(workspace, rel)) // 已空则成；非空（快照有子文件被跳过）则留
		}
	}
	// 第二遍：快照全量覆写。
	_, _, err = copyTree(src, workspace)
	return err
}

// SnapshotPrune 按保留窗口修剪（seq 数值序，保最近 keep 个）。
func SnapshotPrune(dataDir, session string, keep int) error {
	root := snapRoot(dataDir, session)
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var seqs []int
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if n, err := strconv.Atoi(e.Name()); err == nil {
			seqs = append(seqs, n)
		}
	}
	sort.Ints(seqs)
	for i := 0; i < len(seqs)-keep; i++ {
		if err := os.RemoveAll(filepath.Join(root, strconv.Itoa(seqs[i]))); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotRemoveAll 清理会话的全部快照（会话删除时）。
func SnapshotRemoveAll(dataDir, session string) error {
	return os.RemoveAll(snapRoot(dataDir, session))
}
