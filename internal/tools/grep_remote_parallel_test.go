package tools

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowRemoteFS 模拟 RTT 主导的远端：每次 ReadFile 睡 fixed 延迟（真 SSH
// 的形态），并发度经 peak 观察。
type slowRemoteFS struct {
	mu       sync.Mutex
	files    map[string]string
	delay    time.Duration
	inflight int
	peak     int
}

func (f *slowRemoteFS) ReadFile(name string) ([]byte, error) {
	f.mu.Lock()
	f.inflight++
	if f.inflight > f.peak {
		f.peak = f.inflight
	}
	f.mu.Unlock()
	time.Sleep(f.delay)
	f.mu.Lock()
	f.inflight--
	f.mu.Unlock()
	data, ok := f.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(data), nil
}
func (f *slowRemoteFS) Stat(name string) (bool, bool, error) {
	_, ok := f.files[name]
	return ok, false, nil
}
func (f *slowRemoteFS) Glob(pattern string) ([]string, error) {
	var names []string
	for n := range f.files {
		names = append(names, n)
	}
	return names, nil
}

// 其余远端能力不参与 grep——空实现满足接口。
func (f *slowRemoteFS) Exec(string) (ExecOutcome, error)            { return ExecOutcome{}, nil }
func (f *slowRemoteFS) StartBackground(string) (BgProcess, error)   { return nil, nil }
func (f *slowRemoteFS) WriteFile(string, []byte, os.FileMode) error { return nil }
func (f *slowRemoteFS) ReadDir(string) ([]DirEntry, error)          { return nil, nil }
func (f *slowRemoteFS) ReadLink(string) (string, bool, error)       { return "", false, nil }

// TestGrepRemoteParallelFetch 远程 grep 的并发拉取（性能路线）：16 个
// 文件 × 50ms RTT——顺序约 800ms，8 路并发 <400ms；并实测并发峰值 >1
// （确实并发了，且峰值 ≤8 不越上限）。
func TestGrepRemoteParallelFetch(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 16; i++ {
		files[string(rune('a'+i))+".txt"] = "needle line\nplain\n"
	}
	fr := &slowRemoteFS{files: files, delay: 50 * time.Millisecond}
	g := NewGrep(Workspace{Root: "/remote", Remote: fr})

	start := time.Now()
	res := g.Execute(context.Background(), json.RawMessage(`{"pattern":"needle"}`))
	elapsed := time.Since(start)
	if res.IsError {
		t.Fatalf("grep: %s", res.Content)
	}
	if !strings.Contains(res.Content, "16") {
		t.Fatalf("matches missing: %.120s", res.Content)
	}
	if elapsed >= 600*time.Millisecond { // 顺序 800ms；8 路 ≈ 150-250ms（CI 慢机留裕量）
		t.Fatalf("not parallel: %v (peak=%d)", elapsed, fr.peak)
	}
	if fr.peak < 2 || fr.peak > 8 {
		t.Fatalf("concurrency peak = %d, want 2..8", fr.peak)
	}
}
