package service

// 导出元数据头测试（spec 规则 109）：用量/成本行进 HTML 与 MD、未知
// 价省略成本行、谱系链根到叶、旧调用（无 meta）输出不变。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// metaProvider 带 usage 的回合。
type metaProvider struct{}

func (p *metaProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	out := make(chan ai.StreamEvent, 3)
	out <- ai.StreamEvent{Kind: ai.EvMessageStart}
	out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: "答"}
	out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn,
		Usage: ai.Usage{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 2000}}
	close(out)
	return out
}

func TestExportMetaHeader(t *testing.T) {
	dir := t.TempDir()
	// sonnet 有目录价：1000×3 + 500×15 + 2000×0.3 = 3000+7500+600µ$。
	raw := []byte(`{"model":"claude-sonnet-4-5"}`)
	os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600)
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	base, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.UseProviderForTesting(func() ai.Provider { return &metaProvider{} })
	if err := svc.Chat(base.ID, "根问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	forkInfo, err := svc.ForkSession(base.ID, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 根会话：用量与成本（fork 是新账本不带根累计——谱系才继承）。
	htmlPath := filepath.Join(t.TempDir(), "s.html")
	if err := svc.ExportSessionHTML(base.ID, htmlPath); err != nil {
		t.Fatal(err)
	}
	doc, _ := os.ReadFile(htmlPath)
	for _, want := range []string{"用量 ↑1000 ↓500 ⤒缓存读2000 tokens", "≈ 成本 $0.01"} {
		if !strings.Contains(string(doc), want) {
			t.Fatalf("html missing %q:\n%.400s", want, doc)
		}
	}
	forkHTML := filepath.Join(t.TempDir(), "f.html")
	if err := svc.ExportSessionHTML(forkInfo.ID, forkHTML); err != nil {
		t.Fatal(err)
	}
	docF, _ := os.ReadFile(forkHTML)
	if !strings.Contains(string(docF), "谱系 ") {
		t.Fatalf("fork lineage missing:\n%.400s", docF)
	}
	mdPath := filepath.Join(t.TempDir(), "s.md")
	if err := svc.ExportSessionMarkdown(base.ID, mdPath); err != nil {
		t.Fatal(err)
	}
	doc2, _ := os.ReadFile(mdPath)
	if !strings.Contains(string(doc2), "> 用量 ↑1000") || !strings.Contains(string(doc2), "≈ 成本") {
		t.Fatalf("md meta missing:\n%.400s", doc2)
	}
}

func TestExportMetaUnknownPriceOmitsCost(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	svc.DisableAutoMemoryForTesting()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.UseProviderForTesting(func() ai.Provider { return &metaProvider{} })
	if err := svc.Chat(info.ID, "问", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	mdPath := filepath.Join(t.TempDir(), "s.md")
	if err := svc.ExportSessionMarkdown(info.ID, mdPath); err != nil {
		t.Fatal(err)
	}
	doc, _ := os.ReadFile(mdPath)
	if !strings.Contains(string(doc), "用量 ↑1000") {
		t.Fatalf("usage line missing")
	}
	if strings.Contains(string(doc), "≈ 成本") {
		t.Fatalf("unknown price leaked cost line:\n%.200s", doc)
	}
	if strings.Contains(string(doc), "谱系") {
		t.Fatal("root session should have no lineage line")
	}
}
