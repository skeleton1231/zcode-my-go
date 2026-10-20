package service

// 工作区级设置测试（spec 规则 107）：授信后 model/thinking 覆盖生效、
// 未授信不加载、未知键忽略、非法 thinking 兜底 off、远程目标跳过。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// wsSettingsProvider 捕获请求的模型与思考档。
type wsSettingsProvider struct {
	lastReq  ai.Request
	gotModel string
	gotThink ai.ThinkingLevel
}

func (p *wsSettingsProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.lastReq = req
	p.gotModel = req.Model
	p.gotThink = req.Thinking
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "ok"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn}
	close(out)
	return out
}

func wsSettingsEnv(t *testing.T, trusted bool) *AgentService {
	t.Helper()
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".zgo"), 0o755)
	raw, _ := json.Marshal(map[string]string{
		"model": "claude-opus-4-5", "thinking": "high",
		"apiKey": "sk-ant-should-be-ignored", // 白名单外——不认
	})
	os.WriteFile(filepath.Join(ws, ".zgo", "settings.json"), raw, 0o600)
	t.Setenv("ZGO_WS_SETTINGS_DIR", ws)
	if trusted {
		if err := svc.TrustWorkspace(ws, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}

func TestWorkspaceSettingsOverrides(t *testing.T) {
	svc := wsSettingsEnv(t, true)
	ws := os.Getenv("ZGO_WS_SETTINGS_DIR")
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	p := &wsSettingsProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(info.ID, "一问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	if p.gotModel != "claude-opus-4-5" {
		t.Fatalf("model = %q（覆盖未生效）", p.gotModel)
	}
	if p.gotThink != ai.ThinkingHigh {
		t.Fatalf("thinking = %q", p.gotThink)
	}
}

func TestWorkspaceSettingsUntrustedIgnored(t *testing.T) {
	svc := wsSettingsEnv(t, false)
	ws := os.Getenv("ZGO_WS_SETTINGS_DIR")
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	p := &wsSettingsProvider{}
	svc.UseProviderForTesting(func() ai.Provider { return p })
	if err := svc.Chat(info.ID, "一问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	if p.gotModel == "claude-opus-4-5" {
		t.Fatal("untrusted workspace settings applied")
	}
}

func TestWorkspaceSettingsEdge(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	// 无文件 → nil。
	if ov := svc.workspaceSettingsOverrides(t.TempDir()); ov != nil {
		t.Fatalf("no file = %+v", ov)
	}
	// 非法 thinking 兜底 off。
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".zgo"), 0o755)
	os.WriteFile(filepath.Join(ws, ".zgo", "settings.json"),
		[]byte(`{"thinking":"sideways"}`), 0o600)
	svc.TrustWorkspace(ws, "test")
	ov := svc.workspaceSettingsOverrides(ws)
	if ov == nil || ov.Model != "" {
		t.Fatalf("ov = %+v", ov)
	}
	info, _ := svc.NewSession(ws)
	if !svc.SetSessionThinking(info.ID, ov.Thinking) {
		t.Fatal("set thinking failed")
	}
	// ssh:// 目标跳过（不读远端配置）。
	if ov := svc.workspaceSettingsOverrides("ssh://user@h/p"); ov != nil {
		t.Fatal("ssh target loaded")
	}
}
