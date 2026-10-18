package main

import "testing"

// TestParseArgsStrict 验证 issue #35 的四类静默误行为全部变为显式报错，
// 以及别名与位置参数的合法形态。
func TestParseArgsStrict(t *testing.T) {
	ok := func(argv ...string) (prompt, workspace string, fullscreen bool, images []string) {
		p, w, f, imgs, _, _, _, err := parseArgs(argv)
		if err != nil {
			t.Fatalf("parseArgs(%v): %v", argv, err)
		}
		return p, w, f, imgs
	}
	if p, w, f, _ := ok("-p", "hi"); p != "hi" || w != "" || f {
		t.Fatalf("simple -p = %q %q %v", p, w, f)
	}
	if p, _, _, _ := ok("--print", "hi"); p != "hi" {
		t.Fatalf("--print alias = %q", p)
	}
	if _, w, _, _ := ok("-p", "hi", "-C", "D:/tmp"); w != "D:/tmp" {
		t.Fatalf("-C = %q", w)
	}
	if _, _, f, _ := ok("-t"); !f {
		t.Fatal("-t lost")
	}
	if _, _, f, _ := ok("--tui"); !f {
		t.Fatal("--tui lost")
	}
	if p, _, _, _ := ok("直接给提示词"); p != "直接给提示词" {
		t.Fatalf("positional = %q", p)
	}
	if _, _, _, imgs := ok("-p", "hi", "-i", "a.png", "--image", "b.jpg"); len(imgs) != 2 || imgs[0] != "a.png" || imgs[1] != "b.jpg" {
		t.Fatalf("images = %v", imgs)
	}

	bad := map[string][]string{
		"-p 缺值":     {"-p"},
		"-p 吞 flag": {"-p", "-C", "D:/tmp"},
		"未知 flag":   {"--nonexistent-flag"},
		"-C 缺值":     {"-p", "hi", "-C"},
		"-C 吞 flag": {"-C", "-p"},
		"-i 缺值":     {"-p", "hi", "-i"},
		"-i 吞 flag": {"-p", "hi", "-i", "-t"},
		"多余位置参数":    {"hi", "extra"},
	}
	for name, argv := range bad {
		if _, _, _, _, _, _, _, err := parseArgs(argv); err == nil {
			t.Errorf("%s: parseArgs(%v) accepted", name, argv)
		}
	}
}

// TestParseArgsResume 验证 -c/-r 的解析与错误形态（规则 22）。
func TestParseArgsResume(t *testing.T) {
	p, w, f, _, c, r, _, err := parseArgs([]string{"-c"})
	if err != nil || !c || r != "" || p != "" || w != "" || f {
		t.Fatalf("-c = %q %q %v %v %q %v", p, w, f, c, r, err)
	}
	if _, _, _, _, c, r, _, err := parseArgs([]string{"--continue", "-t"}); err != nil || !c || r != "" {
		t.Fatalf("--continue -t = %v %q %v", c, r, err)
	}
	if _, _, _, _, c, r, _, err := parseArgs([]string{"-r", "abc123", "-p", "hi"}); err != nil || c || r != "abc123" {
		t.Fatalf("-r = %v %q %v", c, r, err)
	}
	if _, _, _, _, _, _, _, err := parseArgs([]string{"-r"}); err == nil {
		t.Fatal("bare -r accepted")
	}
	if _, _, _, _, _, _, _, err := parseArgs([]string{"--resume"}); err == nil {
		t.Fatal("bare --resume accepted")
	}
	if _, _, _, _, _, _, _, err := parseArgs([]string{"-r", "-t"}); err == nil {
		t.Fatal("-r swallowing a flag accepted")
	}
}

// TestParseArgsOutputFormat 验证 --output-format 的合法/非法形态（规则
// 23）。
func TestParseArgsOutputFormat(t *testing.T) {
	if _, _, _, _, _, _, f, err := parseArgs([]string{"-p", "hi"}); err != nil || f != "" {
		t.Fatalf("default format = %q %v, want empty (text)", f, err)
	}
	if _, _, _, _, _, _, f, err := parseArgs([]string{"-p", "hi", "--output-format", "json"}); err != nil || f != "json" {
		t.Fatalf("json = %q %v", f, err)
	}
	if _, _, _, _, _, _, f, err := parseArgs([]string{"--output-format", "text"}); err != nil || f != "text" {
		t.Fatalf("text = %q %v", f, err)
	}
	if _, _, _, _, _, _, _, err := parseArgs([]string{"--output-format"}); err == nil {
		t.Fatal("bare --output-format accepted")
	}
	if _, _, _, _, _, _, _, err := parseArgs([]string{"--output-format", "xml"}); err == nil {
		t.Fatal("unknown format accepted")
	}
	if _, _, _, _, _, _, _, err := parseArgs([]string{"--output-format", "-t"}); err == nil {
		t.Fatal("--output-format swallowing a flag accepted")
	}
}
