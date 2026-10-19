package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TestSessionsSearchCLI: hits print with title and snippet; empty query
// rejected; no-hit query reports cleanly.
func TestSessionsSearchCLI(t *testing.T) {
	dir, ids := seedCLI(t)

	code, out := runSessionsCaptured(t, dir, "search", "部署脚本")
	if code != 0 {
		t.Fatalf("code = %d, out = %s", code, out)
	}
	if !strings.Contains(out, "部署脚本排错") || !strings.Contains(out, ids[1][:10]) {
		t.Fatalf("hit missing:\n%s", out)
	}
	if !strings.Contains(out, "会话") || !strings.Contains(out, "片段") {
		t.Fatalf("table header missing:\n%s", out)
	}

	code, out = runSessionsCaptured(t, dir, "search", "不存在的词xyz")
	if code != 0 || !strings.Contains(out, "没有匹配") {
		t.Fatalf("no-hit = %d %q", code, out)
	}

	if code, _ := runSessionsCaptured(t, dir, "search"); code == 0 {
		t.Fatal("empty query accepted")
	}

	// 工具参数参与（种子数据没有工具调用——补一条）。
	appendMsg(t, dir, ids[0], ai.Message{Role: ai.RoleAssistant,
		Blocks: []ai.Block{{Kind: ai.BlockToolCall, Name: "edit",
			Arguments: `{"path":"src/auth/token.go"}`}}})
	code, out = runSessionsCaptured(t, dir, "search", "token.go")
	if code != 0 || !strings.Contains(out, ids[0][:10]) {
		t.Fatalf("tool-args hit missing: %d\n%s", code, out)
	}
}

// appendMsg seeds one more message through a fresh service.
func appendMsg(t *testing.T, dir, id string, m ai.Message) {
	t.Helper()
	svc, err := service.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.AppendForTesting(id, m); err != nil {
		t.Fatal(err)
	}
}

// TestSessionsSanitizeCLI（spec 规则 73）：dry-run 只报数；--apply 落盘
// 后转录无密钥；幂等再跑报「未发现」；未知参数拒绝。
func TestSessionsSanitizeCLI(t *testing.T) {
	dir, ids := seedCLI(t)
	appendMsg(t, dir, ids[0], ai.Message{Role: ai.RoleUser,
		Blocks: []ai.Block{{Kind: ai.BlockText, Text: "k=sk-cli-legacy-abcdefghijklmnopqr"}}})

	code, out := runSessionsCaptured(t, dir, "sanitize")
	if code != 0 || !strings.Contains(out, "dry-run") || !strings.Contains(out, "1 行") {
		t.Fatalf("dry-run = %d %q", code, out)
	}

	code, out = runSessionsCaptured(t, dir, "sanitize", "--apply")
	if code != 0 || !strings.Contains(out, "已清洗 1 行") {
		t.Fatalf("apply = %d %q", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sessions", ids[0]+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-cli-legacy") {
		t.Fatal("secret survived --apply")
	}

	code, out = runSessionsCaptured(t, dir, "sanitize")
	if code != 0 || !strings.Contains(out, "未发现密钥形态") {
		t.Fatalf("idempotent re-run = %d %q", code, out)
	}

	if code, _ := runSessionsCaptured(t, dir, "sanitize", "--bogus"); code == 0 {
		t.Fatal("unknown flag accepted")
	}
}
