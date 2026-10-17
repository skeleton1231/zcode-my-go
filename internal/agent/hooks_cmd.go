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
	PreToolUse  []CommandHook `json:"preToolUse"`
	PostToolUse []CommandHook `json:"postToolUse"`
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
	}
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
