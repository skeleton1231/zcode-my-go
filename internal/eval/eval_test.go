package eval

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunPassAndFail 场景引擎的正反断言：过场景全绿；败场景带失败明细。
func TestRunPassAndFail(t *testing.T) {
	pass := Scenario{
		Name: "pass",
		Turns: []TurnSpec{
			{Tool: "write", Args: []byte(`{"path":"a.txt","content":"内容甲"}`)},
			{Text: "完"},
		},
		Expect: Expectations{
			FileContains:       map[string]string{"a.txt": "内容甲"},
			NoToolErrors:       true,
			TranscriptContains: []string{"wrote a.txt"},
		},
	}
	res := Run(pass)
	if !res.Pass {
		t.Fatalf("pass scenario failed: %+v", res.Failures)
	}
	if res.Requests < 2 {
		t.Fatalf("requests = %d", res.Requests)
	}

	fail := Scenario{
		Name:  "fail",
		Turns: []TurnSpec{{Tool: "read", Args: []byte(`{"path":"nope.txt"}`)}, {Text: "x"}},
		Expect: Expectations{
			FileContains: map[string]string{"nope.txt": "内容"}, // 读不存在的文件不会创建它
			NoToolErrors: true,
		},
	}
	res = Run(fail)
	if res.Pass {
		t.Fatal("fail scenario passed")
	}
	joined := ""
	for _, f := range res.Failures {
		joined += f + "\n"
	}
	if !contains(joined, "nope.txt") || !contains(joined, "工具错误") {
		t.Fatalf("failures = %q", joined)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestLoadDir 目录装载：命名回退、坏文件报错。
func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.json"),
		[]byte(`{"turns":[{"text":"hi"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	scenarios, err := LoadDir(dir)
	if err != nil || len(scenarios) != 1 || scenarios[0].Name != "alpha" {
		t.Fatalf("load = %+v err = %v", scenarios, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("bad json accepted")
	}
}
