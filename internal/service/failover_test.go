package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFallbackModelsSettingRoundtrip 设置面：写盘热生效、读回、去空
// 去重（spec 规则 55）。
func TestFallbackModelsSettingRoundtrip(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if got := svc.FallbackModels(); len(got) != 0 {
		t.Fatalf("initial = %v", got)
	}
	if err := svc.SetFallbackModels([]string{" glm-air ", "", "glm-air", "glm-flash"}); err != nil {
		t.Fatal(err)
	}
	got := svc.FallbackModels()
	if len(got) != 2 || got[0] != "glm-air" || got[1] != "glm-flash" {
		t.Fatalf("got = %v", got)
	}
	// 写盘可回（新实例读同一 dataDir）。
	svc2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	if got2 := svc2.FallbackModels(); len(got2) != 2 || got2[0] != "glm-air" {
		t.Fatalf("reload = %v", got2)
	}
	// 清空即关闭。
	if err := svc.SetFallbackModels(nil); err != nil {
		t.Fatal(err)
	}
	if got := svc.FallbackModels(); len(got) != 0 {
		t.Fatalf("clear = %v", got)
	}
}

// TestFallbackModelsInSettingsFile 落盘形态：settings.json 带数组。
func TestFallbackModelsInSettingsFile(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.SetFallbackModels([]string{"m-b"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fallbackModels") || !strings.Contains(string(data), "m-b") {
		t.Fatalf("settings = %s", string(data))
	}
}
