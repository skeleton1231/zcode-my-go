package tools

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/remote"
	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// remoteWS 启动进程内 SSH 服务器并拨号，返回委托远程的工作区。
// Root 故意指向本地不存在的路径：SSH 服务器服务的磁盘与本地路径解耦，
// 工具若落回本地实现必然报错——只有真正委托远程才能通过。
// remoteWS 启动进程内 SSH 服务器并拨号，返回委托远程的工作区与服务器
// 的真实磁盘根。Root 故意指向本地不存在的路径：SSH 服务器服务的磁盘
// 与本地路径解耦，工具若落回本地实现必然报错——只有真正委托远程才能
// 通过；宿主侧的测试准备（造 symlink 等）直接操作真实根。
func remoteWS(t *testing.T) (Workspace, string) {
	t.Helper()
	srv := remotetest.Start(t)
	host, port, err := net.SplitHostPort(srv.Address)
	if err != nil {
		t.Fatal(err)
	}
	client, err := remote.Dial(remote.Config{
		User: "tester", Host: host, Port: port,
		Key: srv.KeyPEM, Root: srv.Root,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return Workspace{Root: "/nonexistent-local/" + filepath.Base(srv.Root),
		Remote: clientRemote{client}}, srv.Root
}

// clientRemote 适配 remote.Client 到 RemoteFS（测试内联版）。
type clientRemote struct{ c *remote.Client }

func (r clientRemote) Exec(command string) (ExecOutcome, error) {
	res, err := r.c.Exec(command)
	return ExecOutcome{Output: res.Output, ExitCode: res.ExitCode}, err
}
func (r clientRemote) ReadFile(name string) ([]byte, error) { return r.c.ReadFile(name) }
func (r clientRemote) WriteFile(name string, data []byte, perm os.FileMode) error {
	return r.c.WriteFile(name, data, perm)
}
func (r clientRemote) ReadDir(name string) ([]DirEntry, error) {
	entries, err := r.c.ReadDir(name)
	out := make([]DirEntry, len(entries))
	for i, e := range entries {
		out[i] = DirEntry{Name: e.Name, IsDir: e.IsDir}
	}
	return out, err
}
func (r clientRemote) Stat(name string) (bool, bool, error) { return r.c.Stat(name) }
func (r clientRemote) ReadLink(name string) (string, bool, error) {
	return r.c.ReadLink(name)
}
func (r clientRemote) Glob(pattern string) ([]string, error) { return r.c.Glob(pattern) }

// TestEditRemoteApplies 验证 edit 委托远程（review #83：曾落回本地文件
// 系统操作——远程工作区上不一致且可能写本地路径）。
func TestEditRemoteApplies(t *testing.T) {
	ws, _ := remoteWS(t)
	if err := ws.Remote.WriteFile("a.txt", []byte("hello old world"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEdit(ws)
	res := e.Execute(context.Background(), json.RawMessage(
		`{"path":"a.txt","old_string":"old","new_string":"new"}`))
	if res.IsError {
		t.Fatalf("edit: %s", res.Content)
	}
	data, err := ws.Remote.ReadFile("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello new world" {
		t.Fatalf("remote content = %q", data)
	}
}

// TestGrepGlobRemote 验证 grep/glob 委托远程。
func TestGrepGlobRemote(t *testing.T) {
	ws, _ := remoteWS(t)
	_ = ws.Remote.WriteFile("x/a.go", []byte("needle here\n"), 0o644)
	_ = ws.Remote.WriteFile("x/b.go", []byte("nothing\n"), 0o644)

	g := NewGrep(ws)
	res := g.Execute(context.Background(), json.RawMessage(
		`{"pattern":"needle","path":"."}`))
	if res.IsError || !strings.Contains(res.Content, "a.go") {
		t.Fatalf("grep remote = %+v", res)
	}

	gl := NewGlob(ws)
	res = gl.Execute(context.Background(), json.RawMessage(
		`{"pattern":"**/*.go","path":"."}`))
	if res.IsError || !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "b.go") {
		t.Fatalf("glob remote = %+v", res)
	}
}

// TestPatchRemoteAddUpdate 验证 patch 的 add/update 委托远程，delete
// 显式报不支持（RemoteFS 无删除原语）。
func TestPatchRemoteAddUpdate(t *testing.T) {
	ws, _ := remoteWS(t)
	_ = ws.Remote.WriteFile("p.txt", []byte("v1"), 0o644)
	p := NewPatch(ws)
	patchBody := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: p.txt",
		"@@",
		"-v1",
		"+v2",
		"*** Add File: q.txt",
		"+added",
		"*** End Patch",
		"",
	}, "\n")
	raw, err := json.Marshal(map[string]string{"patch": patchBody})
	if err != nil {
		t.Fatal(err)
	}
	res := p.Execute(context.Background(), raw)
	if res.IsError {
		t.Fatalf("patch remote: %s", res.Content)
	}
	if data, _ := ws.Remote.ReadFile("p.txt"); string(data) != "v2" {
		t.Fatalf("p.txt = %q", data)
	}
	if data, _ := ws.Remote.ReadFile("q.txt"); string(data) != "added\n" {
		t.Fatalf("q.txt = %q", data)
	}
}
