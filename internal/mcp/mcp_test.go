package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHelperProcess is not a real test: the test binary re-executes itself
// as a minimal MCP server when invoked with this magic arg.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_MCP_HELPER") != "1" {
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
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo the message",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"message": map[string]any{"type": "string"},
					}},
				}},
			}})
		case "tools/call":
			var full struct {
				Params struct {
					Arguments struct {
						Message string `json:"message"`
					} `json:"arguments"`
				} `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &full)
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "回声：" + full.Params.Arguments.Message}},
			}})
		}
	}
	os.Exit(0)
}

func helperConfig() Config {
	return Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"GO_MCP_HELPER": "1"},
	}
}

// TestServerHandshakeAndCall runs a real server process end to end.
func TestServerHandshakeAndCall(t *testing.T) {
	srv, err := Start(context.Background(), "helper", helperConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	tools := srv.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	out, err := srv.Call(context.Background(), "echo", json.RawMessage(`{"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "回声：hi" {
		t.Fatalf("call output = %q", out)
	}
}

// TestAgentTools verifies the agent.Tool adapter shape.
func TestAgentTools(t *testing.T) {
	srv, err := Start(context.Background(), "helper-2", helperConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	tools := AgentTools(srv)
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	tool := tools[0]
	if tool.Name() != "mcp_helper_2_echo" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "Echo") {
		t.Fatalf("description = %q", tool.Description())
	}
	schema := tool.Parameters()
	if schema["type"] != "object" {
		t.Fatalf("schema = %+v", schema)
	}
	res := tool.Execute(context.Background(), json.RawMessage(`{"message":"go"}`))
	if res.IsError || res.Content != "回声：go" {
		t.Fatalf("execute = %+v", res)
	}
}

// TestStartFailure verifies a broken command surfaces a clean error.
func TestStartFailure(t *testing.T) {
	_, err := Start(context.Background(), "bad", Config{Command: "definitely-not-a-command-xyz"})
	if err == nil {
		t.Fatal("broken command started")
	}
	if !strings.Contains(fmt.Sprint(err), "bad") {
		t.Fatalf("error lacks server name: %v", err)
	}
}

// TestStringIDHelperProcess 是用字符串 JSON-RPC id 应答的假服务器
// （Node MCP SDK 生态常见形态，issue #18 的复现向量）。不是真正的测试。
func TestStringIDHelperProcess(t *testing.T) {
	if os.Getenv("GO_MCP_STRID_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": "resp-init", "result": map[string]any{}})
		case "tools/list":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": "resp-tools", "result": map[string]any{
				"tools": []map[string]any{{
					"name":        "ping",
					"description": "Ping",
					"inputSchema": map[string]any{"type": "object"},
				}},
			}})
		}
	}
	os.Exit(0)
}

// TestStartWithStringIDServer 验证字符串 id 的服务器能完成握手（issue
// #18：响应曾被静默丢弃，挂满 20s 后报误导性的 tools/list 超时）。
func TestStartWithStringIDServer(t *testing.T) {
	cfg := Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStringIDHelperProcess", "--"},
		Env:     map[string]string{"GO_MCP_STRID_HELPER": "1"},
	}
	srv, err := Start(context.Background(), "strid", cfg)
	if err != nil {
		t.Fatalf("start with string-id server: %v", err)
	}
	defer srv.Close()
	if tools := srv.Tools(); len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("tools = %+v", tools)
	}
}

// TestEnvOverrideWins 验证 Config.Env 覆盖父进程同名变量（issue #18 顺带
// 修复：Linux glibc getenv 取第一个匹配，直接 append 的覆盖是无效的）。
func TestEnvOverrideWins(t *testing.T) {
	got := serverEnv(exec.Command("x"), map[string]string{"A": "1", "PATH": "custom"})
	sawPath := ""
	for _, kv := range got {
		if strings.HasPrefix(kv, "PATH=") {
			sawPath = kv
		}
	}
	if sawPath != "PATH=custom" {
		t.Fatalf("PATH override lost: %q", sawPath)
	}
}
