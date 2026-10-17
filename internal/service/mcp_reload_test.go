package service

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

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
			ID     int64  `json:"id"`
			Method string `json:"method"`
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
