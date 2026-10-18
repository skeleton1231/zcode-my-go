package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// toolByName builds the session's real (possibly remote) toolset.
func toolByName(t *testing.T, svc *AgentService, sessionID, name string) agent.Tool {
	t.Helper()
	ws := svc.workspaceFor(sessionID, mustWorkspace(t, svc, sessionID))
	for _, tl := range tools.AllW(ws, nil, nil) {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}

// mustWorkspace reads the session's workspace string back.
func mustWorkspace(t *testing.T, svc *AgentService, sessionID string) string {
	t.Helper()
	for _, s := range svc.ListSessions() {
		if s.ID == sessionID {
			return s.Workspace
		}
	}
	t.Fatal("session not listed")
	return ""
}

// execTool runs one tool call.
func execTool(t *testing.T, tool agent.Tool, args string) agent.ToolResult {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(args))
}

// TestSSHWorkspaceToolsRoundTrip drives the real tool set over the
// in-process SSH server: a session whose workspace is ssh:// reads,
// writes and lists on the host side.
func TestSSHWorkspaceToolsRoundTrip(t *testing.T) {
	srv := remotetest.Start(t)
	t.Setenv("ZGO_SSH_KEY", srv.KeyPEM)
	host := srv.Address[:strings.LastIndexByte(srv.Address, ':')]
	port := srv.Address[strings.LastIndexByte(srv.Address, ':')+1:]
	// 测试服务器直接服务本进程文件系统：root 用它的真实临时目录（转
	// POSIX 斜杠），SFTP 按 / 分隔解析。
	target := "ssh://tester@" + host + ":" + port + "/" + filepath.ToSlash(srv.Root)

	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	info, err := svc.NewSession(target)
	if err != nil {
		t.Fatal(err)
	}
	ws, cleanup, werr := svc.openWorkspace(target)
	if werr != nil {
		t.Fatalf("openWorkspace: %v", werr)
	}
	defer cleanup()
	if !ws.IsRemote() {
		t.Fatal("workspace did not dial")
	}

	// write → host 侧落盘
	if wres := execTool(t, toolByName(t, svc, info.ID, "write"),
		`{"path":"r/a.txt","content":"远程内容"}`); wres.IsError {
		t.Fatalf("write: %s", wres.Content)
	}
	if _, err := os.Stat(filepath.Join(srv.Root, "r", "a.txt")); err != nil {
		t.Fatalf("not on host: %v", err)
	}
	// read → 带行号回来
	res := execTool(t, toolByName(t, svc, info.ID, "read"), `{"path":"r/a.txt"}`)
	if res.IsError || !strings.Contains(res.Content, "远程内容") {
		t.Fatalf("read = %+v", res)
	}
	// ls → 远程目录项
	res = execTool(t, toolByName(t, svc, info.ID, "ls"), `{}`)
	if res.IsError || !strings.Contains(res.Content, "r/") {
		t.Fatalf("ls = %+v", res)
	}
	// bash → 远程执行（测试服务器的 exec 走本地 shell）
	res = execTool(t, toolByName(t, svc, info.ID, "bash"), `{"command":"echo over-ssh"}`)
	if res.IsError || !strings.Contains(res.Content, "over-ssh") {
		t.Fatalf("bash = %+v", res)
	}
	// jail 仍在：越界拒绝（远程路径空间的词法检查）。
	if res := execTool(t, toolByName(t, svc, info.ID, "read"), `{"path":"../../etc/passwd"}`); !res.IsError {
		t.Fatalf("jail escape accepted: %+v", res)
	}
}

// TestRewindGuardedWhileRunning 验证运行守卫先于文件恢复（issue #110：
// restoreCheckpoint 曾在运行检查前执行——活回合的工作树被抽走而转录
// 全保留；cron 后台会话是现实触发方）。
func TestRewindGuardedWhileRunning(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	blocking := make(chan struct{})
	svc.UseProviderForTesting(func() ai.Provider {
		return svcBlockProvider{until: blocking}
	})
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- svc.Chat(info.ID, "start", func(agent.Event) {}) }()
	waitSvcRunning(t, svc, info.ID)
	marker := filepath.Join(ws, "live-marker.txt")
	if err := os.WriteFile(marker, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.RewindLatest(info.ID); err == nil {
		t.Fatal("rewind during run accepted")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("worktree touched during run: %v %q", err, data)
	}
	close(blocking)
	<-done
}

// svcBlockProvider 阻塞到 until 关闭才结束回合（服务层内联版）。
type svcBlockProvider struct{ until chan struct{} }

func (p svcBlockProvider) Stream(ctx context.Context, _ ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 1)
	go func() {
		defer close(out)
		select {
		case <-p.until:
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
		case <-ctx.Done():
		}
	}()
	return out
}

// waitSvcRunning 轮询会话进入运行态。
func waitSvcRunning(t *testing.T, svc *AgentService, id string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if svc.SessionRunning(id) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session never started running")
}

// oneTextProvider 回一段文本后结束（系统提示词断言用的最小 provider）。
type oneTextProvider struct {
	onReq func(ai.Request)
}

func (p oneTextProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	if p.onReq != nil {
		p.onReq(req)
	}
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "ok"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

// TestRemoteSessionSystemContext: 远程会话的系统提示词携带服务器侧根
// 上下文文件（经 RemoteFS 根相对读取——此前 prompt 读本地路径必然落
// 空，远程会话静默丢失项目上下文）。
func TestRemoteSessionSystemContext(t *testing.T) {
	srv := remotetest.Start(t)
	t.Setenv("ZGO_SSH_KEY", srv.KeyPEM)
	host := srv.Address[:strings.LastIndexByte(srv.Address, ':')]
	port := srv.Address[strings.LastIndexByte(srv.Address, ':')+1:]
	target := "ssh://tester@" + host + ":" + port + "/" + filepath.ToSlash(srv.Root)
	if err := os.WriteFile(filepath.Join(srv.Root, "ZGO.md"),
		[]byte("remote workspace rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	var systems []string
	var sysMu sync.Mutex
	svc.UseProviderForTesting(func() ai.Provider {
		// onReq 会从 auto-title 的后台 goroutine 并发到达——裸追加与
		// 主测试的读取竞态（-race 在 CI 上逮过）；锁 + 关自动命名双保险。
		return oneTextProvider{onReq: func(r ai.Request) {
			sysMu.Lock()
			systems = append(systems, r.System)
			sysMu.Unlock()
		}}
	})
	t.Cleanup(func() { svc.UseProviderForTesting(nil) })
	svc.DisableAutoTitleForTesting()

	info, err := svc.NewSession(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "hi", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sys := range systems {
		if strings.Contains(sys, "remote workspace rules") {
			found = true
		}
	}
	if !found {
		t.Fatalf("remote context file missing from system prompt:\n%.400s", systems[0])
	}
}
