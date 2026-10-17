package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// Bash timeouts.
const (
	bashDefaultTimeout = 120 * time.Second
	bashMaxTimeout     = 600 * time.Second
)

// Bash runs a shell command in the workspace. Windows uses PowerShell; Unix
// uses bash (falling back to sh).
type Bash struct{ ws Workspace }

// NewBash builds the bash tool.
func NewBash(ws Workspace) *Bash { return &Bash{ws: ws} }

// Name implements agent.Tool.
func (b *Bash) Name() string { return "bash" }

// Description implements agent.Tool.
func (b *Bash) Description() string {
	return "Run a shell command in the workspace directory. On Windows the " +
		"command runs in PowerShell; elsewhere in bash. stdout and stderr are " +
		"combined, output is truncated to 30KB. timeout_ms caps the run " +
		"(default 120000, max 600000)."
}

// Parameters implements agent.Tool.
func (b *Bash) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"command":    prop("string", "The command line to run"),
		"timeout_ms": prop("number", "Timeout in milliseconds (default 120000, max 600000)"),
	}, "command")
}

// Execute implements agent.Tool.
func (b *Bash) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var a struct {
		Command   string `json:"command"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return errResult("%v", err)
	}
	if a.Command == "" {
		return errResult("command is empty")
	}
	timeout := bashDefaultTimeout
	if a.TimeoutMS > 0 {
		timeout = time.Duration(a.TimeoutMS) * time.Millisecond
		if timeout > bashMaxTimeout {
			timeout = bashMaxTimeout
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, err := b.command(ctx, a.Command)
	if err != nil {
		return errResult("%v", err)
	}
	cmd.Dir = b.ws.Root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Context cancellation (abort) and timeout surface differently: an
		// abort propagates as the run ending; a timeout is a tool error the
		// model can react to.
		if ctx.Err() != nil && errors.Is(err, context.DeadlineExceeded) {
			return errResult("timed out after %s\npartial output:\n%s",
				timeout, truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail))
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return agent.ToolResult{
				IsError: true,
				Content: fmt.Sprintf("exit status %d\n%s",
					exitErr.ExitCode(), truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail)),
			}
		}
		if ctx.Err() == context.Canceled {
			return agent.ToolResult{IsError: true, Content: "canceled"}
		}
		return errResult("running command: %v\n%s", err,
			truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail))
	}
	return agent.ToolResult{Content: truncateBytes(out.Bytes(), bashHeadTail, bashHeadTail)}
}

// command builds the platform shell invocation.
func (b *Bash) command(ctx context.Context, line string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell.exe",
			"-NoProfile", "-NonInteractive", "-Command", line), nil
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return exec.CommandContext(ctx, "bash", "-c", line), nil
	}
	if _, err := exec.LookPath("sh"); err == nil {
		return exec.CommandContext(ctx, "sh", "-c", line), nil
	}
	return nil, fmt.Errorf("no shell found")
}

// Capability implements agent.Capabler: shell commands run anything.
func (b *Bash) Capability() permission.Capability {
	return permission.Capability{Risk: permission.RiskMedium}
}

// CapabilityForArgs implements agent.ArgsCapabler: inspection commands
// (git status, ls, grep…) are read-only and auto-allowed; everything else
// keeps the medium-risk default (ZCode's bash readonly policy).
func (b *Bash) CapabilityForArgs(args string) permission.Capability {
	var a struct {
		Command string `json:"command"`
	}
	cap := b.Capability()
	if json.Unmarshal([]byte(args), &a) == nil && BashCommandReadonly(a.Command) {
		cap.ReadOnly = true
		cap.Risk = permission.RiskLow
	}
	return cap
}
