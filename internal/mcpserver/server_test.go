package mcpserver

// 服务端单测（spec 规则 92）：握手/list/call 形状、只读默认面、
// jail 与错误语义、通知不回、打码。假密钥低熵重复串（GitGuardian）。

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

type resp struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func serveLines(t *testing.T, lines ...string) []resp {
	t.Helper()
	w := tools.Workspace{Root: t.TempDir()}
	var out bytes.Buffer
	s := New(tools.ReadOnlyW(w), &out)
	var input bytes.Buffer
	for _, l := range lines {
		input.WriteString(l + "\n")
	}
	if err := s.Serve(context.Background(), strings.NewReader(input.String())); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resps []resp
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r resp
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parse response %q: %v", line, err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestInitializeShape(t *testing.T) {
	SetVersion("test-1")
	resps := serveLines(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("initialize resp = %+v", resps)
	}
	r := resps[0].Result
	if r.ProtocolVersion != "2024-11-05" || r.ServerInfo.Name != "zgo" ||
		r.ServerInfo.Version != "test-1" {
		t.Fatalf("serverInfo = %+v", r)
	}
	if _, ok := r.Capabilities["tools"]; !ok {
		t.Fatalf("capabilities missing tools: %+v", r.Capabilities)
	}
}

func TestReadOnlyFaceAndList(t *testing.T) {
	resps := serveLines(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	names := map[string]bool{}
	for _, tl := range resps[0].Result.Tools {
		names[tl.Name] = true
		if tl.Description == "" || tl.InputSchema == nil {
			t.Fatalf("tool %s missing schema/description", tl.Name)
		}
	}
	for _, want := range []string{"read", "grep", "glob", "ls"} {
		if !names[want] {
			t.Fatalf("read-only face missing %s: %v", want, names)
		}
	}
	for _, banned := range []string{"write", "edit", "bash", "patch"} {
		if names[banned] {
			t.Fatalf("read-only face exposed %s", banned)
		}
	}
}

func TestCallReadAndJailAndUnknown(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello serve"), 0o600)
	w := tools.Workspace{Root: dir}
	var out bytes.Buffer
	s := New(tools.ReadOnlyW(w), &out)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"note.txt"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"../../etc/passwd"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"bogus/method"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`not json at all`,
	}, "\n") + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("responses = %d（通知不该有响应、坏行只回 parse error）: %v", len(lines), lines)
	}
	var rs [5]resp
	for i, l := range lines {
		json.Unmarshal([]byte(l), &rs[i])
	}
	if rs[0].Error != nil || len(rs[0].Result.Content) != 1 ||
		rs[0].Result.Content[0].Text == "" || !strings.Contains(rs[0].Result.Content[0].Text, "hello serve") {
		t.Fatalf("read call = %+v", rs[0])
	}
	if !rs[1].Result.IsError {
		t.Fatalf("jail escape not error: %+v", rs[1])
	}
	if rs[2].Error == nil || rs[2].Error.Code != -32602 {
		t.Fatalf("unknown tool = %+v", rs[2])
	}
	if rs[3].Error == nil || rs[3].Error.Code != -32601 {
		t.Fatalf("unknown method = %+v", rs[3])
	}
	if rs[4].Error == nil || rs[4].Error.Code != -32700 {
		t.Fatalf("parse error = %+v", rs[4])
	}
}

func TestCallOutputRedacted(t *testing.T) {
	dir := t.TempDir()
	key := "sk-ant-" + strings.Repeat("a", 24)
	os.WriteFile(filepath.Join(dir, "sec.txt"), []byte("token "+key), 0o600)
	w := tools.Workspace{Root: dir}
	var out bytes.Buffer
	s := New(tools.ReadOnlyW(w), &out)
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"sec.txt"}}}` + "\n"
	s.Serve(context.Background(), strings.NewReader(input))
	if strings.Contains(out.String(), key) {
		t.Fatalf("secret leaked: %s", out.String())
	}
}
