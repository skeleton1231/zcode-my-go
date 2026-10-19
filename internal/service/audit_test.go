package service

// 审计穿线（spec 规则 100）：settings.auditLog 开 → 真回合工具执行落
// 审计文件（工具名/参数摘要/成败）；默认关零文件。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

type auditToolProvider struct{ n int }

func (p *auditToolProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.n++
	out := make(chan ai.StreamEvent, 4)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	if p.n%2 == 1 { // 每个回合的工具轮都发一次 bash 调用
		out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: "a1", ToolCallName: "bash"}
		out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: "a1",
			ToolCallArgs: `{"command":"echo hi"}`}
	} else {
		out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "done"}
	}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func TestAuditLogWired(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]bool{"auditLog": true})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	if !svc.AuditLogEnabled() {
		t.Fatal("audit not enabled")
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &auditToolProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(info.ID, "跑一下", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit", "audit-"+time.Now().Format("20060102")+".jsonl")
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("audit file: %v", err)
	}
	if !strings.Contains(string(raw2), `"tool":"bash"`) || !strings.Contains(string(raw2), "echo hi") {
		t.Fatalf("audit record missing:\n%s", raw2)
	}
	if !strings.Contains(string(raw2), info.ID) {
		t.Fatalf("session attribution missing:\n%s", raw2)
	}
}

func TestAuditOffByDefault(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	if svc.AuditLogEnabled() {
		t.Fatal("audit should default off")
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &auditToolProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(info.ID, "x", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	// 关闭时零文件（不建目录不落盘）。
	if _, err := os.Stat(filepath.Join(dir, "audit")); !os.IsNotExist(err) {
		t.Fatal("audit dir created while off")
	}
	// config set 即时生效：开启后下一回合落盘。
	if err := svc.SetAuditLog(true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "再跑", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit", "audit-"+time.Now().Format("20060102")+".jsonl")
	if raw, err := os.ReadFile(path); err != nil || !strings.Contains(string(raw), "bash") {
		t.Fatalf("runtime enable failed: %v %s", err, raw)
	}
}
