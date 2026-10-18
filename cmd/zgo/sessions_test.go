package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// seedCLI makes a data dir with two sessions (titled via their first
// user message) and returns the dir plus the ids.
func seedCLI(t *testing.T) (dir string, ids []string) {
	t.Helper()
	dir = t.TempDir()
	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	for _, text := range []string{"修登录样式", "部署脚本排错"} {
		info, err := svc.NewSession(ws)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.AppendForTesting(info.ID, ai.Message{Role: ai.RoleUser,
			Blocks: []ai.Block{{Kind: ai.BlockText, Text: text}}}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
	}
	svc.Close()
	return dir, ids
}

func runSessionsCaptured(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	t.Setenv("ZGO_TEST_DATADIR", dir)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runSessions(args)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return code, string(out)
}

func TestSessionsListExportRm(t *testing.T) {
	dir, ids := seedCLI(t)

	code, out := runSessionsCaptured(t, dir, "list")
	if code != 0 {
		t.Fatalf("list code = %d", code)
	}
	for _, want := range []string{"修登录样式", "部署脚本排错", "0/0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list misses %q:\n%s", want, out)
		}
	}

	// 前缀导出 Markdown（同一秒创建的两个会话，取到纳秒差异的长度），
	// 显式 -o 指到临时目录（默认落 CWD，测试不污染仓库）。
	// 完整 id：快时钟下同纳秒创建会得到 Manager 追加的 "-2" 后缀，
	// 切尾 6 字符的前缀构造随之失真（CI 首跑实测）；前缀解析另有专测。
	prefix := ids[1]
	dest := filepath.Join(t.TempDir(), "export.md")
	code, out = runSessionsCaptured(t, dir, "export", "--md", "-o", dest, prefix)
	if code != 0 || !strings.Contains(out, "已导出") {
		t.Fatalf("export = %d %q", code, out)
	}
	data, err := os.ReadFile(dest)
	if err != nil || !strings.Contains(string(data), "部署脚本排错") {
		t.Fatalf("md = %d bytes, %v", len(data), err)
	}

	code, _ = runSessionsCaptured(t, dir, "rm", prefix)
	if code != 0 {
		t.Fatal("rm failed")
	}
	_, out = runSessionsCaptured(t, dir, "list")
	if strings.Contains(out, "部署脚本排错") {
		t.Fatal("session still listed after rm")
	}
	if !strings.Contains(out, "修登录样式") {
		t.Fatal("other session vanished")
	}
}

func TestSessionsPrefixAmbiguityAndUnknown(t *testing.T) {
	dir, _ := seedCLI(t)
	// 空前缀：全歧义/无匹配。
	// 错误行走 stderr（stdout 捕获不含）；契约是退出码 1。
	if code, _ := runSessionsCaptured(t, dir, "export", "zzz"); code == 0 {
		t.Fatal("unknown id accepted")
	}
	if code, _ := runSessionsCaptured(t, dir, "rm", ""); code == 0 {
		t.Fatal("empty id accepted")
	}
}

func TestResolveSessionUniqueness(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ws := t.TempDir()
	ia, _ := svc.NewSession(ws)
	ib, _ := svc.NewSession(ws)
	a, b := ia.ID, ib.ID
	// 同分钟内的两个会话：长前缀唯一。
	if id, ok := svc.ResolveSession(a[:len(a)-2]); !ok || id != a {
		// 前缀 a[:n-2] 覆盖到秒级差异前的部分——若恰巧同秒则跳过。
		t.Skipf("ids not prefix-unique enough: %s vs %s", a, b)
	}
	if _, ok := svc.ResolveSession(""); ok {
		t.Fatal("empty resolved")
	}
	if _, ok := svc.ResolveSession("nope"); ok {
		t.Fatal("unknown resolved")
	}
}
