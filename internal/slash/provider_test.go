package slash

// /provider（issue #215）：三形态——无参列表（当前 → 标记）、add 注册、
// 切换即生效回执。

import (
	"strings"
	"testing"
)

// captureProviderOut 直接驱动命令函数并捕获 Out（Run 的 Out 是追加式
// 快照，这里走单次调用拿完整回执）。
func captureProviderOut(t *testing.T, env *Env, line string) string {
	t.Helper()
	var got string
	saved := env.Out
	env.Out = func(msg string) { got = msg }
	defer func() { env.Out = saved }()
	Run(env, line)
	return got
}

func TestSlashProvider(t *testing.T) {
	env, _ := newEnv(t)

	// 无参：列表含 default（首跑迁移）与用法提示。
	out := captureProviderOut(t, env, "/provider")
	if !strings.Contains(out, "default") || !strings.Contains(out, "切换") {
		t.Fatalf("list form: %q", out)
	}

	// add：注册当前配置。
	if out := captureProviderOut(t, env, "/provider add kimi"); !strings.Contains(out, "kimi") {
		t.Fatalf("add form: %q", out)
	}
	// 重复注册报错。
	if out := captureProviderOut(t, env, "/provider add kimi"); !strings.Contains(out, "already exists") {
		t.Fatalf("dup add: %q", out)
	}

	// 切换：回执带剖面名与端点 host。
	if out := captureProviderOut(t, env, "/provider kimi"); !strings.Contains(out, "kimi") {
		t.Fatalf("switch form: %q", out)
	}
	if v := env.Svc.GetSettings(); v.Profile != "kimi" {
		t.Fatalf("switch not effective: %+v", v)
	}

	// 未知名：错误列可用项。
	if out := captureProviderOut(t, env, "/provider ghost"); !strings.Contains(out, "default") ||
		!strings.Contains(out, "kimi") {
		t.Fatalf("unknown form: %q", out)
	}
}
