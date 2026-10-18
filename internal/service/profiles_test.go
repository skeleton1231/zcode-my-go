package service

// 剖面（issue #215，spec desktop-shell 规则 3）：迁移、切换、保存同步、
// key 脱敏边界逐剖面适用。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bootWithSettings 写一份初始设置文件再起服务。env 覆盖清空——resolve()
// 的 ANTHROPIC_* 环境变量优先级高于设置文件，开发机常驻 GLM 代理会污染
// 断言。
func bootWithSettings(t *testing.T, raw string) *AgentService {
	t.Helper()
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if v := os.Getenv("ANTHROPIC_MODEL"); v != "" {
		t.Setenv("ANTHROPIC_MODEL", "")
	}
	dir := t.TempDir()
	if raw != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc
}

// TestProfileMigrationFromFlat 旧版单剖面首启迁移：扁平字段进 default
// 剖面、active=default、扁平字段保持不变（providerFor 路径零感知）。
func TestProfileMigrationFromFlat(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"baseUrl":"https://open.bigmodel.cn/api/anthropic",
		"apiKey":"sk-secret-123","model":"glm-4.7","api":"anthropic","thinking":"low"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	v := svc.GetSettings()
	if v.Profile != "default" {
		t.Fatalf("active profile = %q, want default", v.Profile)
	}
	if v.Model != "glm-4.7" || v.BaseURL != "https://open.bigmodel.cn/api/anthropic" {
		t.Fatalf("flat semantics changed: %+v", v)
	}
	svc.mu.Lock()
	p := svc.settings.Profiles["default"]
	svc.mu.Unlock()
	if p.APIKey != "sk-secret-123" || p.Model != "glm-4.7" || p.Thinking != "low" {
		t.Fatalf("migrated profile: %+v", p)
	}
	// 迁移落盘：直接看文件（同包可见 s.path）。
	data, err := os.ReadFile(svc.path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Profiles map[string]struct {
			Model string `json:"model"`
		} `json:"profiles"`
		Active string `json:"active"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.Active != "default" || f.Profiles["default"].Model != "glm-4.7" {
		t.Fatalf("migration not persisted: %s", data)
	}
}

// TestProfileMigratesEmptyConfig 空配置（首跑）也建 default 剖面——后续
// add 不需要特判「还没有 profiles」。
func TestProfileMigratesEmptyConfig(t *testing.T) {
	svc := bootWithSettings(t, "")
	if v := svc.GetSettings(); v.Profile != "default" {
		t.Fatalf("profile = %q", v.Profile)
	}
}

// TestProviderCommandLifecycle /provider 全生命周期：注册第二个剖面 →
// 切换 → 视图/模型/端点即变；切回；未知名报错列可用项。
func TestProfileSwitch(t *testing.T) {
	svc := bootWithSettings(t, `{"baseUrl":"https://a.example","apiKey":"sk-a","model":"m-a"}`)
	if err := svc.AddProfile("kimi"); err != nil {
		t.Fatal(err)
	}
	v, err := svc.SwitchProfile("kimi")
	if err != nil {
		t.Fatal(err)
	}
	if v.Profile != "kimi" {
		t.Fatalf("switch: %+v", v)
	}
	// 注册时快照的是当时的活动剖面（default/m-a）——切换后视图同源。
	if v.Model != "m-a" || v.BaseURL != "https://a.example" {
		t.Fatalf("kimi should snapshot default's values: %+v", v)
	}
	// 在 kimi 剖面上改模型并换端点：SaveSettings 同步回剖面存储。
	if _, err := svc.SaveSettings("anthropic", "https://k.example", "sk-k", "m-k", "high"); err != nil {
		t.Fatal(err)
	}
	v = svc.GetSettings()
	if v.Profile != "kimi" || v.Model != "m-k" || v.BaseURL != "https://k.example" {
		t.Fatalf("save not applied to kimi: %+v", v)
	}
	svc.mu.Lock()
	p := svc.settings.Profiles["kimi"]
	svc.mu.Unlock()
	if p.APIKey != "sk-k" || p.Model != "m-k" || p.API != "anthropic" {
		t.Fatalf("profile store out of sync: %+v", p)
	}
	// 切回 default：扁平字段即 default 的快照（未被 kimi 的保存污染）。
	v, err = svc.SwitchProfile("default")
	if err != nil {
		t.Fatal(err)
	}
	if v.Model != "m-a" || v.BaseURL != "https://a.example" {
		t.Fatalf("switch back: %+v", v)
	}
	// 未知名：错误列可用项自纠。
	if _, err := svc.SwitchProfile("ghost"); err == nil || !strings.Contains(err.Error(), "kimi") {
		t.Fatalf("unknown profile error: %v", err)
	}
}

// TestProfileKeyNeverCrossesToView 剖面 key 不进视图（同扁平 key 的脱敏
// 边界）；json 序列化视图亦然。
func TestProfileKeyNeverCrossesToView(t *testing.T) {
	svc := bootWithSettings(t, `{"baseUrl":"https://a.example","apiKey":"sk-a","model":"m-a"}`)
	if err := svc.AddProfile("second"); err != nil {
		t.Fatal(err)
	}
	v := svc.GetSettings()
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "sk-a") {
		t.Fatalf("key leaked via view: %s", b)
	}
	if v.HasAPIKey == false {
		t.Fatal("hasApiKey flag lost")
	}
}

// TestAddProfileRejectsBadNames 名字收敛（防路径注入与空白）。
func TestAddProfileRejectsBadNames(t *testing.T) {
	svc := bootWithSettings(t, `{}`)
	for _, name := range []string{"", "a/b", "with space", "默认"} {
		if err := svc.AddProfile(name); err == nil {
			t.Fatalf("bad name accepted: %q", name)
		}
	}
}
