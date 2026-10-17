package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
