package mcpserver

// Dogfood 端到端：internal/mcp 客户端（stdio 拨号）连上本包服务端——
// 生产路径两侧同台验证（握手、tools/list、tools/call）。服务端进程 =
// 测试二进制自重执行（service 侧 MCP helper 同款模式）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/mcp"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// TestMCPServeHelperProcess 不是真测试：化身 `zgo mcp serve`（只读面，
// 工作区来自 ZGO_MCP_SERVE_DIR）。
func TestMCPServeHelperProcess(t *testing.T) {
	if os.Getenv("ZGO_MCP_SERVE_HELPER") != "1" {
		return
	}
	dir := os.Getenv("ZGO_MCP_SERVE_DIR")
	w := tools.Workspace{Root: dir}
	SetVersion("dogfood")
	srv := New(tools.ReadOnlyW(w), os.Stdout).
		WithPrompts(
			func() []skills.Skill {
				return []skills.Skill{{Name: "go-testing", Description: "Go 测试法"}}
			},
			func(name string) (string, bool) {
				if name == "go-testing" {
					return "# Go 测试\n先写失败测试", true
				}
				return "", false
			}).
		WithResources(
			func() []Resource {
				return []Resource{{URI: "zgo://memory/workspace",
					Name: "workspace-memory", Description: "长期记忆"}}
			},
			func(uri string) (string, bool) {
				if uri == "zgo://memory/workspace" {
					return "- 偏好：中文交流", true
				}
				return "", false
			})
	if err := srv.Serve(context.Background(), os.Stdin); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDogfoodClientAgainstServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# dogfood"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := mcp.Start(ctx, "zgo-serve", mcp.Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPServeHelperProcess", "--"},
		Env:     map[string]string{"ZGO_MCP_SERVE_HELPER": "1", "ZGO_MCP_SERVE_DIR": dir},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Close()

	names := map[string]bool{}
	for _, def := range srv.Tools() {
		names[def.Name] = true
	}
	for _, want := range []string{"read", "grep", "glob", "ls"} {
		if !names[want] {
			t.Fatalf("tools/list missing %s: %v", want, names)
		}
	}
	out, err := srv.Call(ctx, "read", json.RawMessage(`{"path":"readme.md"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out, "# dogfood") {
		t.Fatalf("read output = %q", out)
	}

	// 三件套 dogfood 的其余两面（spec 规则 114）：prompts 与
	// resources 经真实客户端往返。
	prompts := srv.Prompts()
	if len(prompts) == 0 {
		t.Fatal("prompts/list empty")
	}
	found := false
	for _, pd := range prompts {
		if pd.Name == "go-testing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("prompts = %+v", prompts)
	}
	pr, err := srv.GetPrompt(ctx, "go-testing", nil)
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if !strings.Contains(pr.PromptText(), "先写失败测试") {
		t.Fatalf("prompt text = %q", pr.PromptText())
	}
	resources := srv.Resources()
	if len(resources) != 1 || resources[0].URI != "zgo://memory/workspace" {
		t.Fatalf("resources = %+v", resources)
	}
	rc, err := srv.ReadResource(ctx, "zgo://memory/workspace")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !strings.Contains(rc.Text, "中文交流") {
		t.Fatalf("resource text = %q", rc.Text)
	}
}
