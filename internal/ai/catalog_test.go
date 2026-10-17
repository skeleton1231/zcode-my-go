package ai

import "testing"

func TestLookupModel(t *testing.T) {
	m := LookupModel("glm-5.2")
	if m.ContextWindow != 200_000 || m.MaxOutput != 32_000 {
		t.Fatalf("glm-5.2 = %+v", m)
	}
	// 族名匹配（带日期后缀）。
	m = LookupModel("claude-sonnet-4-5-20261001")
	if m.ID != "claude-sonnet-4-5" {
		t.Fatalf("family match = %+v", m)
	}
	// 未知模型回退默认。
	m = LookupModel("totally-unknown")
	if m.ContextWindow != DefaultContextWindow || m.MaxOutput != DefaultMaxOutput {
		t.Fatalf("fallback = %+v", m)
	}
}

func TestCatalogModels(t *testing.T) {
	if got := CatalogModels("anthropic"); len(got) < 3 {
		t.Fatalf("anthropic catalog = %d", len(got))
	}
	if got := CatalogModels("openai"); len(got) < 3 {
		t.Fatalf("openai catalog = %d", len(got))
	}
	if got := CatalogModels("gemini"); len(got) < 2 {
		t.Fatalf("gemini catalog = %d", len(got))
	}
}
