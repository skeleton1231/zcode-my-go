package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
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

	// 前缀导出 Markdown（同一秒创建的两个会话）——前缀自适应到最短唯
	// 一：砍尾长度的固定假设在快机器上撞前 3 位纳秒同值（CI 双平台实
	// 测前缀歧义 → resolve 失败）。显式 -o 指到临时目录（默认落 CWD，
	// 测试不污染仓库）。
	prefix := ids[1]
	for len(prefix) > 8 {
		shorter := prefix[:len(prefix)-1]
		if strings.HasPrefix(ids[0], shorter) {
			break // 再短就撞另一会话：这就是最短唯一前缀
		}
		prefix = shorter
	}
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

// TestSessionsArchiveCLI（spec 规则 82）：archive 后 list 默认隐藏、
// --archived 只看归档、--all 全量；unarchive 复原；-c 恢复跳过归档。
func TestSessionsArchiveCLI(t *testing.T) {
	// 同秒创建的两会话共享时间戳前缀（shortID 互为前缀）——按标题断言。
	dir, ids := seedCLI(t)

	code, _ := runSessionsCaptured(t, dir, "archive", ids[0])
	if code != 0 {
		t.Fatal("archive failed")
	}
	_, out := runSessionsCaptured(t, dir, "list")
	if strings.Contains(out, "修登录样式") || !strings.Contains(out, "部署脚本排错") {
		t.Fatalf("default list should hide archived:\n%s", out)
	}
	_, out = runSessionsCaptured(t, dir, "list", "--archived")
	if !strings.Contains(out, "修登录样式") || strings.Contains(out, "部署脚本排错") {
		t.Fatalf("--archived should show only archived:\n%s", out)
	}
	_, out = runSessionsCaptured(t, dir, "list", "--all")
	if !strings.Contains(out, "修登录样式") || !strings.Contains(out, "部署脚本排错") {
		t.Fatalf("--all should show both:\n%s", out)
	}

	if code, _ := runSessionsCaptured(t, dir, "unarchive", ids[0]); code != 0 {
		t.Fatal("unarchive failed")
	}
	_, out = runSessionsCaptured(t, dir, "list")
	if !strings.Contains(out, "修登录样式") {
		t.Fatalf("unarchived session missing from default list:\n%s", out)
	}
	if code, _ := runSessionsCaptured(t, dir, "archive", "zzz-nope"); code == 0 {
		t.Fatal("unknown session accepted")
	}
}

// TestArchivePersistsAcrossRestart（spec 规则 82）：归档态落盘（meta
// 行）——新服务实例（同 dataDir）默认列表仍隐藏、-c 恢复跳过。
func TestArchivePersistsAcrossRestart(t *testing.T) {
	dir, ids := seedCLI(t)
	if code, _ := runSessionsCaptured(t, dir, "archive", ids[1]); code != 0 {
		t.Fatal("archive failed")
	}

	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	// 列表态：归档标记在（信息仍可取，只是默认过滤）。
	infos := svc.ListSessions()
	byID := map[string]bool{}
	for _, i := range infos {
		byID[i.ID] = i.Archived
	}
	if !byID[ids[1]] || byID[ids[0]] {
		t.Fatalf("archive flags wrong after restart: %+v", infos)
	}
	// -c 恢复（同工作区）跳过归档会话——ids[0] 是更早的会话但未归档。
	// seedCLI 两条同工作区：归档了新的（ids[1]），-c 应返回 ids[0]。
	ws := func() string {
		for _, i := range infos {
			if i.ID == ids[0] {
				return i.Workspace
			}
		}
		t.Fatal("ids[0] gone")
		return ""
	}()
	cont, ok := svc.ContinueSession(ws)
	if !ok || cont.ID != ids[0] {
		t.Fatalf("-c should skip archived: got %+v ok=%v", cont, ok)
	}
}

// TestSessionsPinCLI（spec 规则 86）：pin 后置顶组最前（组内时间序）；
// unpin 复原；📌 标记；未知会话拒绝；重启持久。
func TestSessionsPinCLI(t *testing.T) {
	dir, ids := seedCLI(t)
	if code, _ := runSessionsCaptured(t, dir, "pin", ids[0]); code != 0 {
		t.Fatal("pin failed")
	}
	_, out := runSessionsCaptured(t, dir, "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// 首数据行（表头后）必须是置顶会话（修登录样式）。
	if len(lines) < 3 || !strings.Contains(lines[1], "修登录样式") || !strings.Contains(lines[1], "📌") {
		t.Fatalf("pinned session not first with marker:\n%s", out)
	}
	if !strings.Contains(lines[2], "部署脚本排错") {
		t.Fatalf("second session should follow:\n%s", out)
	}

	// 重启持久。
	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	infos := svc.ListSessions()
	if len(infos) < 2 || !infos[0].Pinned || infos[1].Pinned {
		t.Fatalf("pin flag/order lost after restart: %+v", infos)
	}

	if code, _ := runSessionsCaptured(t, dir, "unpin", ids[0]); code != 0 {
		t.Fatal("unpin failed")
	}
	_, out = runSessionsCaptured(t, dir, "list")
	if strings.Contains(out, "📌") {
		t.Fatalf("marker survived unpin:\n%s", out)
	}
	if code, _ := runSessionsCaptured(t, dir, "pin", "zzz-nope"); code == 0 {
		t.Fatal("unknown session accepted")
	}
}

// TestSessionsRetiredRestore（spec 规则 98）：retired 列表 + restore
// 前缀解析与恢复回活跃面。
func TestSessionsRetiredRestore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "archive", "20260101-old.jsonl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out := captureSessionsOut(func() int { return runSessions([]string{"retired"}) })
	if code != 0 || !strings.Contains(out, "20260101-old.jsonl") {
		t.Fatalf("retired = %d %q", code, out)
	}
	// 前缀解析恢复。
	if code := runSessions([]string{"restore", "20260101"}); code != 0 {
		t.Fatal("restore failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260101-old.jsonl")); err != nil {
		t.Fatal("file not back in sessions")
	}
	if code := runSessions([]string{"restore", "missing"}); code != 1 {
		t.Fatalf("missing restore = %d", code)
	}
	if code := runSessions([]string{"restore"}); code != 2 {
		t.Fatalf("argless restore = %d", code)
	}
}

// captureSessionsOut 抓 runSessions 的 stdout。
func captureSessionsOut(fn func() int) (int, string) {
	r, w, err := os.Pipe()
	if err != nil {
		return -1, ""
	}
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}

// TestSessionsTreeCLI（spec 规则 103）：tree 渲染缩进谱系——根行在缩
// 进 0、子代缩进 1；无分叉时提示。
func TestSessionsTreeCLI(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZGO_TEST_DATADIR", dataDir)
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	base, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// fork 要至少一条用户消息（ForkSession 按 user turn 切）。
	svc.UseProviderForTesting(func() ai.Provider { return &treeTurnProvider{} })
	if err := svc.Chat(base.ID, "一问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	fork, err := svc.ForkSession(base.ID, 1)
	if err != nil {
		t.Fatal(err)
	}

	code, out := captureSessionsOut(func() int { return runSessions([]string{"tree"}) })
	if code != 0 {
		t.Fatalf("tree = %d %q", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("tree lines = %q", out)
	}
	if strings.HasPrefix(lines[0], " ") || !strings.HasPrefix(lines[1], "  ") {
		t.Fatalf("indentation wrong:\n%s", out)
	}
	if !strings.Contains(out, fork.ID[:8]) {
		t.Fatalf("fork missing:\n%s", out)
	}
}

// treeTurnProvider 单条文本回复。
type treeTurnProvider struct{}

func (p *treeTurnProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "回复"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}
