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

	"github.com/skeleton1231/zcode-my-go/internal/skills"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

type resp struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		ProtocolVersion string         `json:"protocolVersion"`
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

// TestPromptsFace（spec 规则 111）：prompts/list 与 prompts/get——
// 技能名/描述进清单；正文按名取回（user 消息形态）；未知名报错；
// 未接 WithPrompts 的服务 prompts 方法 404。

func promptsServe(t *testing.T, lines ...string) []string {
	t.Helper()
	var out strings.Builder
	s := New(nil, &out).WithPrompts(
		func() []skills.Skill {
			return []skills.Skill{{Name: "go-testing", Description: "Go 测试法"}, {Name: "debug"}}
		},
		func(name string) (string, bool) {
			if name == "go-testing" {
				return "# Go 测试\n步骤…", true
			}
			return "", false
		})
	input := strings.Join(lines, "\n") + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(out.String()), "\n")
}

func TestPromptsFace(t *testing.T) {
	SetVersion("t")
	lines := promptsServe(t,
		`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"go-testing"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"nope"}}`)
	if len(lines) != 3 {
		t.Fatalf("lines = %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"go-testing"`) || !strings.Contains(lines[0], "Go 测试法") {
		t.Fatalf("list = %s", lines[0])
	}
	if !strings.Contains(lines[1], "# Go 测试") || !strings.Contains(lines[1], `"role":"user"`) {
		t.Fatalf("get = %s", lines[1])
	}
	if !strings.Contains(lines[2], "unknown prompt") {
		t.Fatalf("unknown = %s", lines[2])
	}
	// description 空的技能回落 WhenToUse（此处两者皆空——名字仍在）。
	if !strings.Contains(lines[0], `"debug"`) {
		t.Fatalf("desc-less skill missing: %s", lines[0])
	}
}

func TestPromptsDisabledByDefault(t *testing.T) {
	var out strings.Builder
	s := New(nil, &out)
	err := s.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "prompts not enabled") {
		t.Fatalf("disabled face leaked: %s", out.String())
	}
}

// TestResourcesFace（spec 规则 112）：resources/list 与 read——清单、
// 正文（text/plain contents）、未知 URI、未接面 404。

func TestResourcesFace(t *testing.T) {
	SetVersion("t")
	var out strings.Builder
	s := New(nil, &out).WithResources(
		func() []Resource {
			return []Resource{{URI: "zgo://memory/workspace",
				Name: "workspace-memory", Description: "长期记忆"}}
		},
		func(uri string) (string, bool) {
			if uri == "zgo://memory/workspace" {
				return "- 偏好：中文", true
			}
			return "", false
		})
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"zgo://memory/workspace"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"zgo://nope"}}`,
	}, "\n") + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !strings.Contains(lines[0], "workspace-memory") || !strings.Contains(lines[0], "长期记忆") {
		t.Fatalf("list = %s", lines[0])
	}
	if !strings.Contains(lines[1], "- 偏好：中文") || !strings.Contains(lines[1], "text/plain") {
		t.Fatalf("read = %s", lines[1])
	}
	if !strings.Contains(lines[2], "unknown resource") {
		t.Fatalf("unknown = %s", lines[2])
	}
}

func TestResourcesDisabledByDefault(t *testing.T) {
	var out strings.Builder
	s := New(nil, &out)
	s.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`+"\n"))
	if !strings.Contains(out.String(), "resources not enabled") {
		t.Fatalf("disabled face leaked: %s", out.String())
	}
}
