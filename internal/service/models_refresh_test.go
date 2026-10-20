package service

// RefreshModels 测试（spec 规则 125）：按活动剖面拉清单、内置同名跳
// 过、覆盖层落盘并在下次启动载入；未配置 provider 明确报错。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestRefreshModelsPersistsOverlay(t *testing.T) {
	t.Cleanup(func() { ai.ApplyCatalogOverlay(nil) })
	// 隔离 env 覆盖（resolve() 认 ANTHROPIC_BASE_URL/API_KEY 等——开发
	// 机全局设了 GLM 端点，不隔离会把清单拉到真端点去）。
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ZGO_MODEL", "")
	t.Setenv("ZGO_API", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-9-preview"},{"id":"claude-sonnet-4-5"}]}`))
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	// 活动剖面指向假端点（anthropic 形态）。
	settings := map[string]any{"api": "anthropic", "baseUrl": srv.URL, "apiKey": "k", "model": "glm-9-preview"}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	fetched, added, err := svc.RefreshModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fetched != 2 || added != 1 { // claude-sonnet-4-5 内置已有 → 跳过
		t.Fatalf("fetched=%d added=%d", fetched, added)
	}
	svc.Close()

	if !ai.KnownModel("glm-9-preview") {
		t.Fatal("refreshed id not known in-process")
	}
	if ai.LookupModel("glm-9-preview").ContextWindow != ai.DefaultContextWindow {
		t.Fatal("overlay entry missing default window")
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "models.json"))
	if err != nil {
		t.Fatalf("overlay not persisted: %v", err)
	}
	var f struct {
		Models []ai.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(data, &f); err != nil || len(f.Models) != 1 || f.Models[0].ID != "glm-9-preview" {
		t.Fatalf("overlay file = %s", data)
	}
	// 下次启动载入：新 service 实例仍认识刷新过的 id。
	svc2, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	if !ai.KnownModel("glm-9-preview") {
		t.Fatal("overlay not reloaded on startup")
	}
}

func TestRefreshModelsNoProvider(t *testing.T) {
	t.Cleanup(func() { ai.ApplyCatalogOverlay(nil) })
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, _, err := svc.RefreshModels(context.Background()); err == nil {
		t.Fatal("refresh without provider should fail")
	}
}
