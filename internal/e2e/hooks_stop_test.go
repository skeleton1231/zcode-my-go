package e2e

// Stop / Notification 钩子的全栈 e2e（spec 规则 68，issue #209）：settings
// 配命令 → 真实进程跑钩子。stop 首次阻止收尾（exit 2）、第二次放行——
// 续跑消息进转录、第二回合执行；notification 在权限询问时触发。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// hookBinary 是本测试二进制（自调为钩子命令，跨平台）。
func hookBinary() string { return os.Args[0] }

func writeHookSettings(t *testing.T, dir string, hooks map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newWithHooks 起一个用指定 dataDir 的 harness（settings.json 由测试先
// 落好——New 的 loadSettings 读它）。
func newWithHooks(t *testing.T, dataDir string, turns ...Turn) *Harness {
	t.Helper()
	svc, err := service.New(dataDir)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	t.Cleanup(svc.Close)
	script := wire(t, svc, turns)
	ws := t.TempDir()
	info, err := svc.NewSession(ws)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return &Harness{t: t, Svc: svc, Script: script, SessionID: info.ID, Workspace: ws}
}

// TestE2EStopHookBlocksThenContinues：回合自然收尾被 stop 钩子阻止一次
// （helper 首跑 exit 2、落标记文件后放行），续跑消息进转录、第二回合执
// 行后自然结束（共两次 provider 请求）。
func TestE2EStopHookBlocksThenContinues(t *testing.T) {
	dataDir := t.TempDir()
	marker := filepath.Join(dataDir, "stop-once.flag")
	writeHookSettings(t, dataDir, map[string]any{
		"stop": []map[string]any{{
			"command": hookBinary() + " -test.run=TestE2EStopOnceHelper",
		}},
	})
	t.Setenv("ZGO_E2E_STOP_FLAG", marker)

	h := newWithHooks(t, dataDir,
		Turn{Text: "第一轮干完了"},
		Turn{Text: "继续第二轮"},
	)
	events := h.Ask("干活")

	var streamed strings.Builder
	for _, ev := range events {
		if ev.Kind == agent.EvTextDelta {
			streamed.WriteString(ev.Text)
		}
	}
	if !strings.Contains(streamed.String(), "第一轮") || !strings.Contains(streamed.String(), "第二轮") {
		t.Fatalf("continuation never ran: streamed=%q", streamed.String())
	}
	// 首问 + 续跑一次 = 恰好 2 个请求；第三个请求说明放行失败（脚本
	// 会重放末回合——计数比文本更可靠）。
	if n := len(h.Script.Requests); n != 2 {
		t.Fatalf("provider requests = %d, want 2 (stop hook must allow the second end)", n)
	}
}

// TestE2EStopOnceHelper：首次调用（无标记文件）exit 2 并落标记；之后
// exit 0 放行。
func TestE2EStopOnceHelper(t *testing.T) {
	if os.Getenv("ZGO_E2E_STOP_FLAG") == "" {
		return // 普通 go test 直跑：不是钩子调用
	}
	marker := os.Getenv("ZGO_E2E_STOP_FLAG")
	if _, err := os.Stat(marker); err == nil {
		os.Exit(0)
	}
	os.WriteFile(marker, []byte("1"), 0o644)
	os.Stderr.WriteString("e2e：还有活没干完")
	os.Exit(2)
}

// TestE2ENotificationOnAsk：权限询问触发 notification 钩子（观察者，
// payload 落文件）。
func TestE2ENotificationOnAsk(t *testing.T) {
	dataDir := t.TempDir()
	notifyLog := filepath.Join(dataDir, "notify.log")
	writeHookSettings(t, dataDir, map[string]any{
		"notification": []map[string]any{{
			"command": hookBinary() + " -test.run=TestE2ENotifyHelper",
		}},
	})
	t.Setenv("ZGO_E2E_NOTIFY_LOG", notifyLog)

	h := newWithHooks(t, dataDir,
		Turn{ToolCalls: []ToolCall{{Name: "write", Args: `{"path":"x.txt","content":"hi"}`}}},
		Turn{Text: "done"},
	)
	// 不装自动放行 asker：默认 SetAsker 允许，ask 仍会走一遍通道。
	h.Ask("写文件")

	data, err := os.ReadFile(notifyLog)
	if err != nil || !strings.Contains(string(data), `"tool":"write"`) {
		t.Fatalf("notification not fired on ask: %v %q", err, data)
	}
}

// TestE2ENotifyHelper：payload 落文件（观察者，恒 exit 0）。
func TestE2ENotifyHelper(t *testing.T) {
	if os.Getenv("ZGO_E2E_NOTIFY_LOG") == "" {
		return
	}
	data := make([]byte, 4096)
	n, _ := os.Stdin.Read(data)
	f, err := os.OpenFile(os.Getenv("ZGO_E2E_NOTIFY_LOG"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(3)
	}
	defer f.Close()
	f.Write(append(data[:n], '\n'))
}
