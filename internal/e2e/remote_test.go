package e2e

import (
	"runtime"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// remoteShell 按服务器平台给出命令（remotetest 的 exec 走宿主 shell：
// Windows 是 cmd /c，其余 sh -c）。
func remoteShell(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

// TestE2ERemoteWorkspaceRoundtrip: write 落到 SSH 服务器侧磁盘、read 读回
// ——整条 service→agent→tools→remote 链路第一次进 e2e（此前只有分层的
// remote/tools 单测）。一次 Ask 消耗全部脚本回合（工具结果驱动下一请求）。
func TestE2ERemoteWorkspaceRoundtrip(t *testing.T) {
	h := NewRemote(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "write",
			Args: Marshal(map[string]string{"path": "wire.txt", "content": "over the wire\n"}),
		}}},
		Turn{ToolCalls: []ToolCall{{
			Name: "read",
			Args: Marshal(map[string]string{"path": "wire.txt"}),
		}}},
		Turn{Text: "已读写 wire.txt"},
	)

	events := h.Ask("创建并读取 wire.txt")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.IsError {
			t.Fatalf("tool failed on the wire: %s", ev.ResultContent)
		}
	}
	if got := strings.TrimSpace(h.ReadRemote("wire.txt")); got != "over the wire" {
		t.Fatalf("probe read = %q", got)
	}
	if !HasEvents(events, agent.EvToolExecutionEnd, "over the wire") {
		t.Fatal("read tool did not return the remote content")
	}
}

// TestE2ERemotePathJail: 越界路径（../ 逃逸与绝对路径）在远程同样被词法
// jail 拒绝——工具错误回到转录，服务器侧文件不受影响。
func TestE2ERemotePathJail(t *testing.T) {
	outside := "jail-outside.txt"
	h := NewRemote(t,
		Turn{ToolCalls: []ToolCall{
			{Name: "write", Args: Marshal(map[string]string{
				"path": "../" + outside, "content": "escaped\n"})},
			{Name: "read", Args: Marshal(map[string]string{"path": "/etc/passwd"})},
		}},
		Turn{Text: "两个越界调用都应失败"},
	)

	events := h.Ask("越界写与读")
	var denied int
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.IsError {
			denied++
		}
	}
	if denied != 2 {
		t.Fatalf("expected 2 jailed tool errors, got %d: %+v", denied, events)
	}
	if h.RemoteHas("../" + outside) {
		t.Fatal("escape write landed outside the remote root")
	}
}

// TestE2ERemoteBashWritesFile: 同步 bash 在远端执行并落盘（cd 根、合并
// 输出回转录）。
func TestE2ERemoteBashWritesFile(t *testing.T) {
	h := NewRemote(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "bash",
			Args: Marshal(map[string]string{"command": remoteShell(
				`echo remote-pulse > pulse.txt && echo made`,
				`echo remote-pulse> pulse.txt & echo made`)}),
		}}},
		Turn{Text: "pulse.txt 已创建"},
	)

	events := h.Ask("远端创建 pulse.txt")
	if !HasEvents(events, agent.EvToolExecutionEnd, "made") {
		t.Fatal("bash output did not return over the wire")
	}
	if got := h.ReadRemote("pulse.txt"); !strings.Contains(got, "remote-pulse") {
		t.Fatalf("remote pulse file = %q", got)
	}
}

// TestE2ERemoteBackgroundJobLifecycle: 后台任务的三段生命周期全走网线——
// 启动不阻塞回合（后续工具立即执行）、同步等待垫保证后台 echo 落盘后
// 可见、bash_kill 后再查即已退出。kill 独占一个回合：同一模型回合的工
// 具并行执行，与后台任务抢跑会把它截杀在启动前。
func TestE2ERemoteBackgroundJobLifecycle(t *testing.T) {
	h := NewRemote(t,
		Turn{ToolCalls: []ToolCall{{
			Name: "bash",
			Args: Marshal(map[string]any{
				"command": remoteShell(
					`echo bg-alive > pulse.txt && sleep 30`,
					`echo bg-alive> pulse.txt & ping -n 30 127.0.0.1 > nul`),
				"background": true}),
		}}},
		Turn{ToolCalls: []ToolCall{
			// 同步等待垫：给后台 echo 落盘留出确定性窗口，再读回证据。
			{Name: "bash", Args: Marshal(map[string]string{"command": remoteShell(
				`sleep 1 && cat pulse.txt`,
				`ping -n 2 127.0.0.1 > nul & type pulse.txt`)})},
			{Name: "bash_output", Args: Marshal(map[string]string{"job_id": "job1"})},
		}},
		Turn{ToolCalls: []ToolCall{{
			Name: "bash_kill", Args: Marshal(map[string]string{"job_id": "job1"}),
		}}},
		Turn{ToolCalls: []ToolCall{{
			Name: "bash_output", Args: Marshal(map[string]string{"job_id": "job1"}),
		}}},
		Turn{Text: "后台任务已停止"},
	)

	events := h.Ask("启动后台任务并停止")
	if !HasEvents(events, agent.EvAgentEnd, "") {
		t.Fatalf("run did not end: %+v", events)
	}
	if !HasEvents(events, agent.EvToolExecutionEnd, "后台任务已启动") {
		t.Fatal("background start did not return a job id")
	}
	if !HasEvents(events, agent.EvToolExecutionEnd, "bg-alive") {
		t.Fatal("background echo not visible via sync wait-and-read")
	}
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.IsError {
			t.Fatalf("lifecycle failed: %s", ev.ResultContent)
		}
	}
	if !HasEvents(events, agent.EvToolExecutionEnd, "已退出") {
		t.Fatal("job not exited after bash_kill")
	}
	if !h.RemoteHas("pulse.txt") {
		t.Fatal("background pulse file missing on the remote side")
	}
}
