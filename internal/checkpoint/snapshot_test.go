package checkpoint

// 文件快照存储（spec 规则 83）的单元：镜像恢复（改/删/增三向）、保
// 留窗口、上限降级、符号链接跳过、空目录壳清理。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func snapEnv(t *testing.T) (dataDir, ws string) {
	t.Helper()
	return t.TempDir(), t.TempDir()
}

func snapWrite(t *testing.T, ws, rel, content string) {
	t.Helper()
	p := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapRead(t *testing.T, ws, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSnapshotRoundTrip(t *testing.T) {
	dataDir, ws := snapEnv(t)
	snapWrite(t, ws, "a.txt", "v1")
	snapWrite(t, ws, "sub/b.go", "package sub")
	if err := SnapshotCapture(dataDir, ws, "s1", 1); err != nil {
		t.Fatal(err)
	}

	// 改 + 删 + 增三向污染。
	snapWrite(t, ws, "a.txt", "CHANGED")
	if err := os.Remove(filepath.Join(ws, "sub", "b.go")); err != nil {
		t.Fatal(err)
	}
	snapWrite(t, ws, "later.txt", "added after snapshot")

	if err := SnapshotRestore(dataDir, ws, "s1", 1); err != nil {
		t.Fatal(err)
	}
	if got := snapRead(t, ws, "a.txt"); got != "v1" {
		t.Fatalf("modified file not restored: %q", got)
	}
	if got := snapRead(t, ws, "sub/b.go"); got != "package sub" {
		t.Fatalf("deleted file not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "later.txt")); !os.IsNotExist(err) {
		t.Fatal("post-snapshot file survived restore")
	}
}

func TestSnapshotPruneAndErrors(t *testing.T) {
	dataDir, ws := snapEnv(t)
	// 旧会话无快照：Missing。
	if err := SnapshotRestore(dataDir, ws, "old", 1); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("missing = %v", err)
	}
	snapWrite(t, ws, "a.txt", "x")
	for seq := 1; seq <= Retention+2; seq++ {
		if err := SnapshotCapture(dataDir, ws, "s2", seq); err != nil {
			t.Fatal(err)
		}
	}
	// 保留窗口外：Pruned。
	if err := SnapshotRestore(dataDir, ws, "s2", 1); !errors.Is(err, ErrSnapshotPruned) {
		t.Fatalf("pruned = %v", err)
	}
	// 窗口内最近一个照常。
	if err := SnapshotRestore(dataDir, ws, "s2", Retention+2); err != nil {
		t.Fatal(err)
	}
	// RemoveAll 清光。
	if err := SnapshotRemoveAll(dataDir, "s2"); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotRestore(dataDir, ws, "s2", Retention+2); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("after RemoveAll = %v", err)
	}
}

func TestSnapshotCapsDegrade(t *testing.T) {
	dataDir, ws := snapEnv(t)
	for i := 0; i < SnapMaxFiles+5; i++ {
		snapWrite(t, ws, "d/"+pad(i)+".txt", "x")
	}
	err := SnapshotCapture(dataDir, ws, "big", 1)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("caps = %v", err)
	}
	// 半截快照不留。
	if _, err := os.Stat(filepath.Join(dataDir, "snapshots", "big")); !os.IsNotExist(err) {
		t.Fatal("partial snapshot left behind")
	}
}

func TestSnapshotSkipsNoiseAndSymlinks(t *testing.T) {
	dataDir, ws := snapEnv(t)
	snapWrite(t, ws, "real.txt", "keep")
	snapWrite(t, ws, ".DS_Store", "noise")
	if err := os.Symlink("real.txt", filepath.Join(ws, "link.txt")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := SnapshotCapture(dataDir, ws, "s3", 1); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, "snapshots", "s3", "1")
	if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !os.IsNotExist(err) {
		t.Fatal("system noise snapshotted")
	}
	if _, err := os.Stat(filepath.Join(root, "link.txt")); !os.IsNotExist(err) {
		t.Fatal("symlink snapshotted (should skip)")
	}
}

func pad(n int) string {
	s := "0000" + itoa(n)
	return s[len(s)-5:]
}

func itoa(n int) string { // 测试内联 strconv
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
