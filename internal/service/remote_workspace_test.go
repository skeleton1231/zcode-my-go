package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
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
