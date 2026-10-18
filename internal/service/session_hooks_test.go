package service

// 会话生命周期钩子的 service 级端到端（spec 规则 24）：settings 配置外
// 部命令，NewSession/CompactSession/Close/DeleteSession 各触点真实跑子
// 进程。宿主是本测试二进制的自重调（与 agent 包同款模式）。

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSvcSessionHelper：观察者宿主——stdin 追加到 ZGO_HOOK_FILE。
func TestSvcSessionHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "session" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	f, err := os.OpenFile(os.Getenv("ZGO_HOOK_FILE"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(0)
	}
	f.Write(append(data, '\n'))
	f.Close()
	os.Exit(0)
}

// TestSvcCompactBlockHelper：preCompact 阻止者。
func TestSvcCompactBlockHelper(t *testing.T) {
	if os.Getenv("ZGO_HOOK_MODE") != "block-compact" {
		return
	}
	os.Stderr.WriteString("策略禁止压缩")
	os.Exit(1)
}

func svcHookCommand(helper string) string {
	return os.Args[0] + " -test.run=" + helper
}

func waitForFileContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil &&
			strings.Contains(string(data), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("hook log never contained %q: %q", want, data)
}

// TestSessionLifecycleHooks：settings 注入三个钩子——新建触发
// sessionStart（异步，轮询文件）、压缩被 preCompact 阻止并回带原因、
// Close 对存活会话触发 sessionEnd（reason=close）。
func TestSessionLifecycleHooks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hooks.log")
	t.Setenv("ZGO_HOOK_FILE", file)
	t.Setenv("ZGO_HOOK_MODE", "session")

	settings := fmt.Sprintf(`{"hooks":{
		"sessionStart":[{"command":%q}],
		"sessionEnd":[{"command":%q}],
		"preCompact":[{"command":%q}]
	}}`,
		svcHookCommand("TestSvcSessionHelper"),
		svcHookCommand("TestSvcSessionHelper"),
		svcHookCommand("TestSvcSessionHelper"))
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitForFileContains(t, file, info.ID)

	// 原服务退出：sessionEnd（close）触发——此时观察者模式还开着。
	svc.Close()
	waitForFileContains(t, file, `"reason":"close"`)

	// preCompact 阻止：换阻止者模式与设置，另起服务（同一 dataDir 与
	// 会话集）压缩同一会话。
	t.Setenv("ZGO_HOOK_MODE", "block-compact")
	blocking := fmt.Sprintf(`{"hooks":{"preCompact":[{"command":%q}]}}`,
		svcHookCommand("TestSvcCompactBlockHelper"))
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(blocking), 0o600); err != nil {
		t.Fatal(err)
	}
	svc2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	if err := svc2.CompactSession(info.ID); err == nil ||
		!strings.Contains(err.Error(), "策略禁止压缩") {
		t.Fatalf("compact not blocked: %v", err)
	}
}
