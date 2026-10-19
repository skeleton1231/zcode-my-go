package e2e

// 遥测子系统 e2e（spec 规则 91）：settings.json 预写 telemetry.enabled
// （settings 在 New 时加载——#193 教训，写在前），真 service/session/
// loop 跑一个工具回合 + 手动压缩，断言 traces JSONL 的 span 形状与
// 「密钥不进遥测」红线。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func traceSpans(t *testing.T, dataDir string) []map[string]any {
	t.Helper()
	path := filepath.Join(dataDir, "traces", "trace-"+time.Now().Format("20060102")+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse span: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func TestE2ETelemetrySpansAndNoSecrets(t *testing.T) {
	dataDir := t.TempDir()
	key := "sk-ant-" + strings.Repeat("a", 24) // 低熵假密钥（GitGuardian）
	settings := map[string]any{
		"apiKey":    key,
		"telemetry": map[string]any{"enabled": true},
	}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	script := wire(t, svc, []Turn{
		{ToolCalls: []ToolCall{{Name: "write", Args: Marshal(map[string]string{
			"path": "telemetry-note.txt", "content": "遥测内容",
		})}}},
		{Text: "已写入", Usage: ai.Usage{InputTokens: 11, OutputTokens: 4}},
		// 压缩需要 8 条消息（DefaultKeepTail=6 + 可摘要前缀）：脚本
		// 后续回合作填充与摘要。
		{Text: "回答一"},
		{Text: "回答二"},
		{Text: "回答三"},
		{Text: "回答四"},
		{Text: "这是摘要"},
	})
	_ = script
	ws := t.TempDir()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Chat(info.ID, "写个笔记", func(agent.Event) {}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	// 4 问凑满 8 条消息（同 TestE2ECompactKeepsTail 的形态）。
	for i := 1; i <= 4; i++ {
		if err := svc.Chat(info.ID, "填充"+strings.Repeat("问", i), func(agent.Event) {}); err != nil {
			t.Fatalf("fill chat %d: %v", i, err)
		}
	}

	spans := traceSpans(t, dataDir)
	byName := map[string]int{}
	turnIDs := map[string]bool{}
	for _, s := range spans {
		byName[s["name"].(string)]++
		if s["name"] == "agent.turn" {
			turnIDs[s["spanId"].(string)] = true
		}
	}
	// 5 个回合（首次工具回合 + 4 个填充问），首个回合 2 次模型调用。
	if byName["agent.turn"] != 5 {
		t.Fatalf("agent.turn = %d (%v)", byName["agent.turn"], byName)
	}
	if byName["model.call"] != 6 || byName["tool.call"] != 1 {
		t.Fatalf("span counts = %v", byName)
	}
	if len(turnIDs) != 5 {
		t.Fatalf("turn ids = %d", len(turnIDs))
	}
	for _, s := range spans {
		if n, ok := s["name"].(string); ok && (n == "model.call" || n == "tool.call") {
			if p, _ := s["parentSpanId"].(string); !turnIDs[p] {
				t.Fatalf("%s not under a turn span: %v", n, s["parentSpanId"])
			}
		}
	}
	// 手动压缩（spec 规则 91 的第四类 span）。
	if err := svc.CompactSession(info.ID); err != nil {
		t.Fatalf("compact: %v", err)
	}
	spans = traceSpans(t, dataDir)
	found := false
	for _, s := range spans {
		if s["name"] == "session.compact" {
			found = true
		}
	}
	if !found {
		t.Fatal("session.compact span missing")
	}

	// 红线：settings 里的 key 与工具输出内容都不出现在遥测文件。
	path := filepath.Join(dataDir, "traces", "trace-"+time.Now().Format("20060102")+".jsonl")
	raw2, _ := os.ReadFile(path)
	if strings.Contains(string(raw2), key) {
		t.Fatal("api key leaked into telemetry file")
	}
	if strings.Contains(string(raw2), "遥测内容") {
		t.Fatal("tool output leaked into telemetry file")
	}
}
