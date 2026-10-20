package main

// zgo eval --json（spec 规则 120）：结构化报告——stdout 只出 JSON
//（CI 消费面），含 total/passed/failed 与逐场景结果；文本模式不变。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEvalJSONReport(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pass.json"), []byte(`{
		"name": "json-pass",
		"description": "通过场景",
		"turns": [{"text": "好的"}]
	}`), 0o644)
	os.WriteFile(filepath.Join(dir, "fail.json"), []byte(`{
		"name": "json-fail",
		"description": "失败场景",
		"turns": [{"text": "不行"}],
		"expect": {"transcript_contains": ["永不出现的文本"]}
	}`), 0o644)
	t.Setenv("ZGO_TEST_DATADIR", t.TempDir())

	code, out := captureEvalOut(func() int { return runEval([]string{dir, "--json"}) })
	if code != 1 {
		t.Fatalf("code = %d（有失败场景应为 1）", code)
	}
	var report struct {
		Total   int `json:"total"`
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Results []struct {
			Name     string   `json:"Name"`
			Pass     bool     `json:"Pass"`
			Failures []string `json:"Failures"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if report.Total != 2 || report.Passed != 1 || report.Failed != 1 {
		t.Fatalf("summary = %+v", report)
	}
	// LoadDir 按文件名序：fail.json 在 pass.json 之前。
	if len(report.Results) != 2 || report.Results[0].Pass || !report.Results[1].Pass {
		t.Fatalf("results = %+v", report.Results)
	}
	if len(report.Results[0].Failures) == 0 {
		t.Fatal("failures missing in JSON")
	}
}

// captureEvalOut 抓 runEval 的 stdout。
func captureEvalOut(fn func() int) (int, string) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := fn()
	os.Stdout = old
	w.Close()
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for n, err := r.Read(buf); err == nil && n > 0; n, err = r.Read(buf) {
		data = append(data, buf[:n]...)
	}
	return code, string(data)
}
