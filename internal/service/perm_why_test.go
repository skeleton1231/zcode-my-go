package service

// PermWhy 测试（spec 规则 129）：预演与 loop gate 同链路——只读放行、
// 中风险询问、预批准站放行、未知工具拒绝、plan 模式拦截改动类。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

func TestPermWhy(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 只读工具：能力兜底放行。
	why, err := svc.PermWhy(info.ID, "read", json.RawMessage(`{"path":"a.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(why, "allow") || !strings.Contains(why, "能力兜底") {
		t.Fatalf("read why = %q", why)
	}

	// bash 参数化能力：只读命令（ls）放行、副作用命令（rm）询问。
	why, err = svc.PermWhy(info.ID, "bash", json.RawMessage(`{"command":"rm scratch.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(why, "ask") {
		t.Fatalf("bash why = %q", why)
	}

	// 参数化能力（webfetch）：预批准文档站只读放行，未知主机询问。
	for args, want := range map[string]string{
		`{"url":"https://go.dev/pkg"}`: "allow",
		`{"url":"https://example.com"}`: "ask",
	} {
		why, err = svc.PermWhy(info.ID, "webfetch", json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(why, want) {
			t.Fatalf("webfetch %s → want %s:\n%s", args, want, why)
		}
	}

	// 未知工具：明确报错。
	if _, err := svc.PermWhy(info.ID, "nosuchtool", nil); err == nil {
		t.Fatal("unknown tool accepted")
	}

	// plan 模式：改动类工具被拒（只读不受影响）。
	perm := svc.mgr.SessionPermissions(info.ID)
	if perm == nil {
		t.Fatal("permissions not wired")
	}
	perm.SetMode(permission.ModePlan)
	defer perm.SetMode(permission.ModeNormal)
	why, _ = svc.PermWhy(info.ID, "write", json.RawMessage(`{"path":"a.txt","content":"x"}`))
	if !strings.Contains(why, "deny") || !strings.Contains(why, "plan") {
		t.Fatalf("plan why = %q", why)
	}
}
