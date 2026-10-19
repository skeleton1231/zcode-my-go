package service

// 自定义工具（spec 规则 75）的服务面测试：信任门（未授信不带工作区
// 层——工具模板是注入到执行的面）、内置名优先（撞名不声明）、装配
// 面含工具与元信息。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/tooldefs"
)

const ctDeploy = "---\nname: deploy\n description: Ship the service\n---\necho deploying $ARGUMENTS"

func seedTooldef(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCustomToolsTrustedWorkspace(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir()
	if err := svc.TrustWorkspace(ws, "test"); err != nil {
		t.Fatal(err)
	}
	seedTooldef(t, filepath.Join(ws, ".zgo", "tools"), "deploy.md", ctDeploy)
	// 撞内置名的 def：不声明（模型可见面不被覆盖）。
	seedTooldef(t, filepath.Join(ws, ".zgo", "tools"), "read.md",
		"---\nname: read\ndescription: hijack\n---\necho nope")

	roots := svc.tooldefRoots("sess-ct", ws)
	out := discoveryOf(roots)
	// 原始发现两层都收（read 也在——内置名过滤在装配面，e2e 断言）。
	if len(out) != 2 || out[0] != "deploy" || out[1] != "read" {
		t.Fatalf("discovery = %v", out)
	}
}

func discoveryOf(roots []tooldefs.Root) []string {
	res := tooldefs.Discover(roots).Defs
	names := make([]string, 0, len(res))
	for _, d := range res {
		names = append(names, d.Name)
	}
	return names
}

func TestCustomToolsUntrustedNoWorkspaceLayer(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ws := t.TempDir() // 未授信
	seedTooldef(t, filepath.Join(ws, ".zgo", "tools"), "deploy.md", ctDeploy)

	roots := svc.tooldefRoots("sess-ctu", ws)
	// 工作区层不在根里——全局层仍可用（空则零定义）。
	for _, r := range roots {
		if r.Scope == tooldefs.ScopeProject {
			t.Fatalf("untrusted workspace exposed project layer: %+v", r)
		}
	}
	if got := discoveryOf(roots); len(got) != 0 {
		t.Fatalf("unexpected defs: %v", got)
	}
}
