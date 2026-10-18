package slash

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestModelCommandWarnsOnUnknown 验证未知模型 ID 立刻得到警告（issue
// #61：手滑拼写曾是成功假象，下次 Chat 才 400）。警告不阻断——README
// 承诺任意模型 ID，自定义端点的非目录模型是合法用法。
func TestModelCommandWarnsOnUnknown(t *testing.T) {
	env, out := newEnv(t)

	// 完全无家族前缀的拼写错误：警告可见。
	Run(env, "/model cloude-sonnet-4-5")
	if out.Len() == 0 || !strings.Contains(out.All()[out.Len()-1], "未识别") {
		t.Fatalf("unknown model warning = %v", out.All())
	}
	// 家族变体（带日期后缀）不警告。
	Run(env, "/model glm-5.2-air-20261101")
	if last := out.All()[out.Len()-1]; strings.Contains(last, "未识别") {
		t.Fatalf("family variant warned: %q", last)
	}
	// 目录模型不警告。
	Run(env, "/model glm-5.2")
	if strings.Contains(out.All()[out.Len()-1], "未识别") {
		t.Fatal("catalog model warned")
	}
}

// TestKnownModel 直接验证识别语义。
func TestKnownModel(t *testing.T) {
	for _, id := range []string{"glm-5.2", "glm-5.2-air-20261101", "claude-sonnet-4-5"} {
		if !ai.KnownModel(id) {
			t.Errorf("KnownModel(%q) = false", id)
		}
	}
	for _, id := range []string{"", "cloude-sonnet", "totally-made-up"} {
		if ai.KnownModel(id) {
			t.Errorf("KnownModel(%q) = true", id)
		}
	}
}
