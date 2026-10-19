package checkpoint

// 远程快照（spec 规则 84）：内存 RemoteFS 假件驱动捕获/镜像恢复/错
// 误分层（真实 SSH 环境由 e2e 的 remotetest 覆盖路径面）。

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// memRemote 是内存远程工作区（根相对斜杠路径）。
type memRemote struct {
	files map[string]string // rel path -> content
	dirs  map[string]bool
	execs []string
}

func newMemRemote() *memRemote {
	return &memRemote{files: map[string]string{}, dirs: map[string]bool{"": true}}
}

func (m *memRemote) put(rel, content string) {
	m.files[rel] = content
	for d := filepath.ToSlash(filepath.Dir(rel)); d != "." && d != "/"; d = filepath.ToSlash(filepath.Dir(d)) {
		m.dirs[d] = true
	}
}

func (m *memRemote) ReadFile(name string) ([]byte, error) {
	if c, ok := m.files[name]; ok {
		return []byte(c), nil
	}
	return nil, os.ErrNotExist
}

func (m *memRemote) WriteFile(name string, data []byte, perm os.FileMode) error {
	m.put(name, string(data))
	return nil
}

func (m *memRemote) ReadDir(name string) ([]tools.DirEntry, error) {
	if !m.dirs[name] && name != "" {
		return nil, os.ErrNotExist
	}
	seen := map[string]bool{}
	var out []tools.DirEntry
	for rel := range m.files {
		if name == "" {
			if !strings.Contains(rel, "/") {
				if !seen[rel] {
					out = append(out, tools.DirEntry{Name: rel, IsDir: false})
					seen[rel] = true
				}
				continue
			}
			top := strings.SplitN(rel, "/", 2)[0]
			if !seen[top] {
				out = append(out, tools.DirEntry{Name: top, IsDir: true})
				seen[top] = true
			}
			continue
		}
		prefix := name + "/"
		if strings.HasPrefix(rel, prefix) {
			rest := rel[len(prefix):]
			if !strings.Contains(rest, "/") {
				if !seen[rest] {
					out = append(out, tools.DirEntry{Name: rest, IsDir: false})
					seen[rest] = true
				}
				continue
			}
			top := strings.SplitN(rest, "/", 2)[0]
			if !seen[top] {
				out = append(out, tools.DirEntry{Name: top, IsDir: true})
				seen[top] = true
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memRemote) Exec(command string) (tools.ExecOutcome, error) {
	m.execs = append(m.execs, command)
	// rm -f '<rel>' 形态。
	if strings.HasPrefix(command, "rm -f '") {
		rel := strings.TrimSuffix(strings.TrimPrefix(command, "rm -f '"), "'")
		delete(m.files, rel)
	}
	return tools.ExecOutcome{ExitCode: 0, Output: ""}, nil
}

func TestRemoteSnapshotRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	r := newMemRemote()
	r.put("a.txt", "v1")
	r.put("sub/b.go", "package sub")
	if err := SnapshotCaptureRemote(dataDir, r, "rs1", 1); err != nil {
		t.Fatal(err)
	}

	// 改 + 删 + 增。
	r.put("a.txt", "CHANGED")
	delete(r.files, "sub/b.go")
	r.put("later.txt", "post")

	if err := SnapshotRestoreRemote(dataDir, r, "rs1", 1); err != nil {
		t.Fatal(err)
	}
	if r.files["a.txt"] != "v1" {
		t.Fatalf("modified not restored: %q", r.files["a.txt"])
	}
	if r.files["sub/b.go"] != "package sub" {
		t.Fatal("deleted not restored")
	}
	if _, ok := r.files["later.txt"]; ok {
		t.Fatal("post-snapshot file survived remote restore")
	}
}

func TestRemoteSnapshotErrorLayers(t *testing.T) {
	dataDir := t.TempDir()
	r := newMemRemote()
	r.put("a.txt", "x")
	if err := SnapshotRestoreRemote(dataDir, r, "none", 1); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("missing = %v", err)
	}
	for seq := 1; seq <= Retention+1; seq++ {
		if err := SnapshotCaptureRemote(dataDir, r, "rs2", seq); err != nil {
			t.Fatal(err)
		}
	}
	if err := SnapshotRestoreRemote(dataDir, r, "rs2", 1); !errors.Is(err, ErrSnapshotPruned) {
		t.Fatalf("pruned = %v", err)
	}
	if err := SnapshotRestoreRemote(dataDir, r, "rs2", Retention+1); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSnapshotSkipsNoiseAndQuotes(t *testing.T) {
	dataDir := t.TempDir()
	r := newMemRemote()
	r.put("keep.txt", "k")
	r.put(".DS_Store", "n")
	if err := SnapshotCaptureRemote(dataDir, r, "rs3", 1); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, "snapshots", "rs3", "1")
	if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !os.IsNotExist(err) {
		t.Fatal("noise captured")
	}
	// 恢复时带空格路径的删除走单引号包裹。
	r.put("my file.txt", "post-snapshot")
	if err := SnapshotRestoreRemote(dataDir, r, "rs3", 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.files["my file.txt"]; ok {
		t.Fatal("spaced path not removed")
	}
	quoted := false
	for _, c := range r.execs {
		if strings.Contains(c, "'my file.txt'") {
			quoted = true
		}
	}
	if !quoted {
		t.Fatalf("rm not quoted: %v", r.execs)
	}
}
