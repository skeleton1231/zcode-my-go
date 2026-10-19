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

// TestRunNotContains（安全回归网第二批）：transcript_not_contains 命中
// 即失败、不命中即通过——负向守卫的两面。
func TestRunNotContains(t *testing.T) {
	leak := Scenario{
		Name: "leak",
		Turns: []TurnSpec{
			{Tool: "write", Args: []byte(`{"path":"a.txt","content":"k=sk-ant-cccccccccccccccccccccccc"}`)},
			{Tool: "read", Args: []byte(`{"path":"a.txt"}`)},
			{Text: "x"},
		},
		Expect: Expectations{TranscriptNotContains: []string{"sk-ant-cccc"}},
	}
	// write 的结果是摘要、read 的结果经打码——裸密钥都不该在转录里。
	res := Run(leak)
	if !res.Pass {
		t.Fatalf("clean transcript flagged: %+v", res.Failures)
	}

	forced := Scenario{
		Name:   "forced",
		Turns:  []TurnSpec{{Text: "正常回复"}},
		Expect: Expectations{TranscriptNotContains: []string{"正常回复"}},
	}
	res = Run(forced)
	if res.Pass {
		t.Fatal("negative guard did not fire on present string")
	}
}
