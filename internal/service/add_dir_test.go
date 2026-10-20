package service

// AddWorkspaceDir 测试（spec 规则 128）：并入后工具真读得到、幂等/上
// 限/远程拒绝、WorkspaceDirs 清单。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

func TestAddWorkspaceDir(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	root := t.TempDir()
	info, err := svc.NewSession(root)
	if err != nil {
		t.Fatal(err)
	}
	extra := t.TempDir()
	if err := os.WriteFile(filepath.Join(extra, "peer.md"), []byte("邻仓文档"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := svc.AddWorkspaceDir(info.ID, extra)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != extra {
		t.Fatalf("dirs = %v", dirs)
	}
	// 幂等：同目录再添不重复。
	if dirs, _ = svc.AddWorkspaceDir(info.ID, extra); len(dirs) != 1 {
		t.Fatalf("dup accepted: %v", dirs)
	}
	if got := svc.WorkspaceDirs(info.ID); len(got) != 2 {
		t.Fatalf("WorkspaceDirs = %v", got)
	}

	// 并入后缓存里的 Workspace 带 Extra——按它构建的 read 工具真读得
	// 到附加根里的文件（工具面每回合经 workspaceFor 取同源缓存）。
	ws := svc.workspaceFor(info.ID, root)
	if len(ws.Extra) != 1 || ws.Extra[0] != extra {
		t.Fatalf("cached ws extra = %v", ws.Extra)
	}
	readTool := tools.NewRead(ws)
	res := readTool.Execute(context.Background(),
		json.RawMessage(`{"path":`+quoteJSONAbs(filepath.Join(extra, "peer.md"))+`}`))
	if res.IsError || !strings.Contains(res.Content, "邻仓文档") {
		t.Fatalf("read via extra root: %+v", res)
	}
}

// TestAddWorkspaceDirLimits：上限 8；未知会话拒绝。
func TestAddWorkspaceDirLimits(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxExtraDirs; i++ {
		if _, err := svc.AddWorkspaceDir(info.ID, filepath.Join(t.TempDir(), "d")); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := svc.AddWorkspaceDir(info.ID, filepath.Join(t.TempDir(), "overflow")); err == nil {
		t.Fatal("cap not enforced")
	}
	if _, err := svc.AddWorkspaceDir("no-such-session", "."); err == nil {
		t.Fatal("unknown session accepted")
	}
}

func quoteJSONAbs(p string) string {
	b, _ := json.Marshal(p)
	return string(b)
}
