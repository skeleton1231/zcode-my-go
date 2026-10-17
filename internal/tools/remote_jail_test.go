package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteSymlinkEscapeRejected 验证远程侧指向 jail 外的符号链接被
// 拒绝（issue #89：resolveReal 是本地调用，远程回退纯词法——#4 的远程
// 等价物）。链接在 SSH 服务器侧真实创建。
func TestRemoteSymlinkEscapeRejected(t *testing.T) {
	ws, srvRoot := remoteWS(t)
	outside := t.TempDir() // 与服务器根不同的目录
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 链接直接造在宿主磁盘（服务器经 SFTP 服务同一磁盘）：shell 的
	// ln -s 在 msys 下是复制语义；junction 无需特权（#4 同型向量）。
	if err := os.MkdirAll(filepath.Join(srvRoot, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, outside, filepath.Join(srvRoot, "links", "leakdir"))
	read := NewRead(ws)
	res := read.Execute(context.Background(), json.RawMessage(`{"path":"links/leakdir/secret.txt"}`))
	if !res.IsError {
		t.Fatalf("read through remote escaping link allowed: %s", res.Content)
	}
	if strings.Contains(res.Content, "SECRET") {
		t.Fatal("jail-outside content leaked through remote link")
	}
	write := NewWrite(ws)
	res = write.Execute(context.Background(), json.RawMessage(
		`{"path":"links/leakdir/written.txt","content":"ESCAPED"}`))
	if !res.IsError {
		t.Fatal("write through remote escaping link allowed")
	}
	if _, err := os.Stat(filepath.Join(outside, "written.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote outside the remote jail")
	}
}

// TestRemoteInternalSymlinkAllowed 验证指向 jail 内的远程链接仍可用
// （monorepo 场景不误伤）。
func TestRemoteInternalSymlinkAllowed(t *testing.T) {
	ws, srvRoot := remoteWS(t)
	if err := ws.Remote.WriteFile("real/a.txt", []byte("in-jail"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, filepath.Join(srvRoot, "real"), filepath.Join(srvRoot, "alias"))
	read := NewRead(ws)
	res := read.Execute(context.Background(), json.RawMessage(`{"path":"alias/a.txt"}`))
	if res.IsError || !strings.Contains(res.Content, "in-jail") {
		t.Fatalf("internal remote link rejected: %s", res.Content)
	}
}
