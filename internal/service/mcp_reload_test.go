package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/mcp"
)

// TestMCPHelperProcess 不是真测试：测试二进制化身最小 MCP 服务器。
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("ZGO_SVC_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		case "tools/list":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"tools": []map[string]any{{"name": "ping"}}}})
		case "resources/list":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"resources": []map[string]any{{
					"uri": "config://app", "name": "应用配置",
					"description": "服务器暴露的配置", "mimeType": "text/plain",
				}}}})
		case "resources/read":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"contents": []map[string]any{{
					"uri": "config://app", "mimeType": "text/plain",
					"text": "debug=false" + "\\n" + "retries=3",
				}}}})
		case "prompts/list":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"prompts": []map[string]any{{
					"name": "review", "description": "评审当前改动",
					"arguments": []map[string]any{{"name": "focus", "required": true}},
				}}}})
		case "prompts/get":
			var p struct {
				Arguments map[string]string `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			text := "请评审当前改动"
			if p.Arguments["focus"] != "" {
				text += "，重点：" + p.Arguments["focus"]
			}
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"description": "评审当前改动",
				"messages": []map[string]any{
					{"role": "user", "content": map[string]any{"type": "text", "text": text}},
					{"role": "assistant", "content": map[string]any{"type": "text", "text": "收到，我会逐条检查。"}},
				}}})
		}
	}
	os.Exit(0)
}

func helperMCPConfig(extra string) map[string]mcp.Config {
	return map[string]mcp.Config{
		"helper": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestMCPHelperProcess", "--"},
			Env:     map[string]string{"ZGO_SVC_MCP_HELPER": "1", "EXTRA": extra},
		},
	}
}

// TestMCPHotReload 验证 syncMCP：新增启动、签名不变不重启、移除关闭。
func TestMCPHotReload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/settings.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 新增 → 启动并带签名。
	s.mu.Lock()
	s.settings.MCPServers = helperMCPConfig("a")
	s.mu.Unlock()
	s.syncMCP()
	s.mu.Lock()
	servers := append([]*mcp.Server(nil), s.mcp...)
	s.mu.Unlock()
	if len(servers) != 1 || servers[0].Name != "helper" || servers[0].Signature == "" {
		t.Fatalf("after add: %+v", servers)
	}
	if tools := servers[0].Tools(); len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("handshake tools = %+v", tools)
	}

	// 同签名再同步：同一对象（未重启）。
	s.syncMCP()
	s.mu.Lock()
	same := len(s.mcp) == 1 && s.mcp[0] == servers[0]
	s.mu.Unlock()
	if !same {
		t.Fatal("unchanged config restarted the server")
	}

	// 移除 → 清空。
	s.mu.Lock()
	s.settings.MCPServers = nil
	s.mu.Unlock()
	s.syncMCP()
	s.mu.Lock()
	n := len(s.mcp)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("after remove: %d", n)
	}
}

// TestMCPSignature 验证签名对 command/args/env 敏感且 env 序稳定。
func TestMCPSignature(t *testing.T) {
	base := mcp.Config{Command: "go", Args: []string{"run", "x"}, Env: map[string]string{"A": "1", "B": "2"}}
	if mcpSignature(base) != mcpSignature(mcp.Config{Command: "go", Args: []string{"run", "x"},
		Env: map[string]string{"B": "2", "A": "1"}}) {
		t.Fatal("env order flipped the signature")
	}
	changed := base
	changed.Args = []string{"run", "y"}
	if mcpSignature(base) == mcpSignature(changed) {
		t.Fatal("args change not reflected")
	}
}

// TestMCPPrompts 验证 MCP 提示词面（spec 规则 28）：helper 服务器的
// 提示词可列出（参数标记）、prompts/get 展开含实参与 assistant 段。
func TestMCPPrompts(t *testing.T) {
	dir := t.TempDir()
	settings := fmt.Sprintf(`{"mcpServers":{"helper":{"command":%q,`+
		`"args":["-test.run=TestMCPHelperProcess","--"],`+
		`"env":{"ZGO_SVC_MCP_HELPER":"1"}}}}`, os.Args[0])
	if err := os.WriteFile(dir+"/settings.json", []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(svc.MCPPrompts()) == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	entries := svc.MCPPrompts()
	if len(entries) != 1 || entries[0].Server != "helper" || entries[0].Prompt.Name != "review" {
		t.Fatalf("prompts = %+v", entries)
	}
	if len(entries[0].Prompt.Arguments) != 1 || !entries[0].Prompt.Arguments[0].Required {
		t.Fatalf("prompt args = %+v", entries[0].Prompt.Arguments)
	}

	text, err := svc.MCPGetPrompt("helper", "review", map[string]string{"focus": "性能"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"请评审当前改动，重点：性能", "[assistant] 收到"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if _, err := svc.MCPGetPrompt("nope", "review", nil); err == nil {
		t.Fatal("unknown server accepted")
	}
}

// TestMCPResources 验证 MCP 资源面（spec 规则 31）：列出（uri/描述）、
// resources/read 读取文本、未知服务器拒绝。
func TestMCPResources(t *testing.T) {
	dir := t.TempDir()
	settings := fmt.Sprintf(`{"mcpServers":{"helper":{"command":%q,`+
		`"args":["-test.run=TestMCPHelperProcess","--"],`+
		`"env":{"ZGO_SVC_MCP_HELPER":"1"}}}}`, os.Args[0])
	if err := os.WriteFile(dir+"/settings.json", []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(svc.MCPResources()) == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	entries := svc.MCPResources()
	if len(entries) != 1 || entries[0].Server != "helper" || entries[0].Resource.URI != "config://app" {
		t.Fatalf("resources = %+v", entries)
	}
	if entries[0].Resource.Description == "" {
		t.Fatal("resource description lost")
	}

	text, err := svc.MCPReadResource("helper", "config://app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "debug=false") || !strings.Contains(text, "retries=3") {
		t.Fatalf("resource text = %q", text)
	}
	if _, err := svc.MCPReadResource("nope", "config://app"); err == nil {
		t.Fatal("unknown server accepted")
	}
}
