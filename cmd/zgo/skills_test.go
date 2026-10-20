package main

// zgo skills CLI 测试（spec 规则 115）：list 列工作区技能与内置层、
// show 打正文、未知名退出码 1、缺参退出码 2；未授信工作区不列其层。

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillsCapture(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return code, string(data)
}

func TestSkillsCLI(t *testing.T) {
	t.Setenv("ZGO_TEST_DATADIR", t.TempDir())
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".zgo", "skills", "deploy-steps"), 0o755)
	os.WriteFile(filepath.Join(ws, ".zgo", "skills", "deploy-steps", "SKILL.md"),
		[]byte("---\nname: deploy-steps\ndescription: 部署三步法\n---\n先跑测试再上线。\n"), 0o644)
	t.Setenv("ZGO_SKILLS_TEST_WS", ws)

	// 授信前：工作区层不出现（信任门）——但内置层仍在。
	code, out := skillsCapture(func() int { return runSkills([]string{"list", ws}) })
	if code != 0 || strings.Contains(out, "deploy-steps") {
		t.Fatalf("untrusted ws layer leaked: %d\n%s", code, out)
	}
	if !strings.Contains(out, "内置") {
		t.Fatalf("builtin layer missing:\n%s", out)
	}
	// 授信后：工作区层出现。
	if c := runTrust([]string{ws}); c != 0 {
		t.Fatalf("trust = %d", c)
	}
	code, out = skillsCapture(func() int { return runSkills([]string{"list", ws}) })
	if code != 0 {
		t.Fatalf("list = %d", code)
	}
	if !strings.Contains(out, "deploy-steps") || !strings.Contains(out, "部署三步法") || !strings.Contains(out, "工作区") {
		t.Fatalf("list missing skill:\n%s", out)
	}

	// show：内置技能可读（不依赖工作区授信）。
	code, out = skillsCapture(func() int { return runSkills([]string{"show", "go-testing"}) })
	if code != 0 || !strings.Contains(out, "测试") && len(out) < 10 {
		t.Fatalf("show builtin = %d len=%d", code, len(out))
	}
	if code := runSkills([]string{"show", "nope"}); code != 1 {
		t.Fatalf("unknown = %d", code)
	}
	if code := runSkills([]string{"show"}); code != 2 {
		t.Fatalf("argless show = %d", code)
	}
	if code := runSkills([]string{"bogus"}); code != 2 {
		t.Fatalf("bogus subcmd = %d", code)
	}
}
