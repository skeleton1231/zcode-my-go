package agent

// userPromptSubmit 外部命令钩子（spec 规则 96）：exit 2 拦截（输出为
// 原因）、JSON stdout 改写、其他退出码观察放行。测试二进制自重执行
//（hooks_test 同款 env 模式）。

import (
	"encoding/json"
	"os"
	"testing"
)

// TestHookPromptHelper 不是真测试：按 ZGO_HOOK_MODE 决定行为。
func TestHookPromptHelper(t *testing.T) {
	switch os.Getenv("ZGO_HOOK_MODE") {
	case "prompt-block":
		os.Stderr.WriteString("提示包含敏感词，已拦截")
		os.Exit(2)
	case "prompt-rewrite":
		var p map[string]any
		json.NewDecoder(os.Stdin).Decode(&p)
		os.Stdout.WriteString(`{"prompt":"[规范前缀] ` + p["prompt"].(string) + `"}`)
		os.Exit(0)
	case "prompt-crash":
		os.Exit(3)
	default:
		return // 普通 go test 直跑：不是 hook 调用
	}
}

func TestPromptHookFromCommands(t *testing.T) {
	cmd := os.Args[0] + " -test.run=TestHookPromptHelper"
	hook := func() func(PromptContext) PromptResult {
		return promptFromCommands([]CommandHook{{Command: cmd}})
	}

	os.Setenv("ZGO_HOOK_MODE", "prompt-block")
	t.Cleanup(func() { os.Unsetenv("ZGO_HOOK_MODE") })
	if r := hook()(PromptContext{Prompt: "看这个 key"}); !r.Block || r.Reason != "提示包含敏感词，已拦截" {
		t.Fatalf("block result = %+v", r)
	}

	os.Setenv("ZGO_HOOK_MODE", "prompt-rewrite")
	if r := hook()(PromptContext{Prompt: "修一下"}); r.Block || r.Replacement != "[规范前缀] 修一下" {
		t.Fatalf("rewrite result = %+v", r)
	}

	// 非零非 2：记日志放行（不改写、不拦截）。
	os.Setenv("ZGO_HOOK_MODE", "prompt-crash")
	if r := hook()(PromptContext{Prompt: "x"}); r.Block || r.Replacement != "" {
		t.Fatalf("crash result = %+v", r)
	}

	if promptFromCommands(nil) != nil {
		t.Fatal("empty hooks must be nil")
	}
}
