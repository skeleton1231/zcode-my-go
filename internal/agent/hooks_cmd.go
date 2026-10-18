package agent

// External command hooks (ZCode's core/hooks semantics): commands from
// settings run per tool call. A preToolUse command receives the call as
// JSON on stdin — a non-zero exit blocks it (stderr carries the reason).
// A postToolUse command receives the result — exit 2 appends its stderr
// to the result as feedback for the model; other exits only observe.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// hookTimeout bounds each external hook command.
const hookTimeout = 10 * time.Second

// CommandHook is one configured external hook.
type CommandHook struct {
	Command string   `json:"command"`
	Tools   []string `json:"tools"` // empty = every tool
}

// CommandHooks is the settings shape.
type CommandHooks struct {
	PreToolUse   []CommandHook `json:"preToolUse"`
	PostToolUse  []CommandHook `json:"postToolUse"`
	SessionStart []CommandHook `json:"sessionStart"`
	SessionEnd   []CommandHook `json:"sessionEnd"`
	PreCompact   []CommandHook `json:"preCompact"`
	Stop         []CommandHook `json:"stop"`         // spec 规则 68
	Notification []CommandHook `json:"notification"` // spec 规则 68
}

// matches reports whether the hook applies to a tool.
func (h CommandHook) matches(tool string) bool {
	if len(h.Tools) == 0 {
		return true
	}
	for _, t := range h.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

// HooksFromCommands wires configured commands into loop hooks.
func HooksFromCommands(cfg CommandHooks) Hooks {
	return Hooks{
		BeforeToolCall: beforeFromCommands(cfg.PreToolUse),
		AfterToolCall:  afterFromCommands(cfg.PostToolUse),
		Stop:           stopFromCommands(cfg.Stop),
	}
}

// stopFromCommands 把 stop 命令接成 loop 的收尾询问（spec 规则 69）：
// exit 2 阻止收尾（输出为原因，注入续跑消息）；其他退出码记日志放行
// ——对齐 Claude Code 的 exit-2 语义。
func stopFromCommands(hooks []CommandHook) func(StopContext) StopResult {
	if len(hooks) == 0 {
		return nil
	}
	return func(c StopContext) StopResult {
		payload := stopPayloadJSON(c)
		for _, h := range hooks {
			out, code, err := runHook(h.Command, payload)
			if err != nil {
				log.Printf("stop hook %q: %v", h.Command, err)
				continue
			}
			if code == 2 {
				reason := strings.TrimSpace(out)
				if reason == "" {
					reason = "stop hook requested continuation"
				}
				return StopResult{Blocked: true, Reason: reason}
			}
			if code != 0 {
				log.Printf("stop hook %q: exit %d: %s", h.Command, code,
					strings.TrimSpace(out))
			}
		}
		return StopResult{}
	}
}

// stopPayloadJSON 组装 stop 事件 payload（spec 规则 68 的字段契约）。
func stopPayloadJSON(c StopContext) []byte {
	payload, _ := json.Marshal(map[string]any{
		"event":     "stop",
		"sessionId": c.SessionID,
		"workspace": c.Workspace,
		"lastText":  c.LastText,
	})
	return payload
}

// NotificationHooks runs notification hooks（观察者：退出码不影响主流
// 程失败记日志）——权限询问即将阻塞等待用户时触发（spec 规则 69）.
func NotificationHooks(hooks []CommandHook, payload any) {
	runObserverHooks("notification", hooks, payload)
}

func beforeFromCommands(hooks []CommandHook) func(BeforeToolCallContext) BeforeToolCallResult {
	if len(hooks) == 0 {
		return nil
	}
	return func(c BeforeToolCallContext) BeforeToolCallResult {
		payload, _ := json.Marshal(map[string]any{
			"tool": c.ToolCall.Name,
			"args": json.RawMessage(c.Args),
		})
		for _, h := range hooks {
			if !h.matches(c.ToolCall.Name) {
				continue
			}
			out, code, err := runHook(h.Command, payload)
			if err != nil {
				// 命令本身失败（找不到/超时）：观察并放行，不拦运行，
				// 但不静默——记日志（AGENTS.md：不吞错）。
				log.Printf("preToolUse hook %q: %v", h.Command, err)
				continue
			}
			if code != 0 {
				reason := strings.TrimSpace(out)
				return BeforeToolCallResult{Block: true, Reason: reason}
			}
		}
		return BeforeToolCallResult{}
	}
}

func afterFromCommands(hooks []CommandHook) func(AfterToolCallContext) AfterToolCallResult {
	if len(hooks) == 0 {
		return nil
	}
	return func(c AfterToolCallContext) AfterToolCallResult {
		payload, _ := json.Marshal(map[string]any{
			"tool":    c.ToolCall.Name,
			"result":  c.Result.Content,
			"isError": c.Result.IsError,
		})
		for _, h := range hooks {
			if !h.matches(c.ToolCall.Name) {
				continue
			}
			out, code, err := runHook(h.Command, payload)
			if err != nil {
				log.Printf("postToolUse hook %q: %v", h.Command, err)
				continue
			}
			if code != 2 {
				continue
			}
			feedback := strings.TrimSpace(out)
			if feedback == "" {
				continue
			}
			content := c.Result.Content + "\n[hook 反馈] " + feedback
			return AfterToolCallResult{Content: content,
				IsError: c.Result.IsError, Changed: true}
		}
		return AfterToolCallResult{}
	}
}

// runHook runs one command with the payload on stdin; stderr+stdout
// combined as its message.
func runHook(command string, payload []byte) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if runtime.GOOS == "windows" {
		// 不用 powershell -Command：它会把原生命令的非零退出码重写为 1
		// （exit 2 的反馈语义会丢）；cmd /c 原样传播退出码与参数。
		cmd = exec.CommandContext(ctx, "cmd", "/c", command)
	}
	cmd.Stdin = bytes.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out.String(), exitErr.ExitCode(), nil
		}
		return "", 0, err
	}
	return out.String(), 0, nil
}

// SessionStartHooks runs sessionStart hooks（观察者：payload JSON 上
// stdin；失败或非零退出记日志，绝不影响会话——启动钩子不该能把工具
// 变砖）.
func SessionStartHooks(hooks []CommandHook, payload any) {
	runObserverHooks("sessionStart", hooks, payload)
}

// SessionEndHooks runs sessionEnd hooks（同观察者语义）.
func SessionEndHooks(hooks []CommandHook, payload any) {
	runObserverHooks("sessionEnd", hooks, payload)
}

// PreCompactHooks runs preCompact hooks before compaction; the first
// non-zero exit aborts it（其 stderr 作为原因回给用户）——压缩时机
// 可被策略约束.
func PreCompactHooks(hooks []CommandHook, payload any) (blocked bool, reason string) {
	if len(hooks) == 0 {
		return false, ""
	}
	data, _ := json.Marshal(payload)
	for _, h := range hooks {
		out, code, err := runHook(h.Command, data)
		if err != nil {
			log.Printf("preCompact hook %q: %v", h.Command, err)
			continue
		}
		if code != 0 {
			return true, strings.TrimSpace(out)
		}
	}
	return false, ""
}

// runObserverHooks runs a lifecycle hook list, logging every failure
// (AGENTS.md：不吞错——观察者也不静默).
func runObserverHooks(point string, hooks []CommandHook, payload any) {
	if len(hooks) == 0 {
		return
	}
	data, _ := json.Marshal(payload)
	for _, h := range hooks {
		out, code, err := runHook(h.Command, data)
		if err != nil {
			log.Printf("%s hook %q: %v", point, h.Command, err)
			continue
		}
		if code != 0 {
			log.Printf("%s hook %q: exit %d: %s", point, h.Command, code,
				strings.TrimSpace(out))
		}
	}
}
