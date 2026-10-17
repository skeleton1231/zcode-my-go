package main

// The terminal surfaces share internal/slash's command table; this file
// only adapts it to the REPL/TUI environment.

import (
	"fmt"
	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/slash"
	"os"
)

// commandEnv carries what the terminal commands act on.
type commandEnv struct {
	svc       *service.AgentService
	info      *agent.SessionInfo
	workspace string
	out       func(string)
}

// runSlashCommand adapts slash.Run for the terminal surfaces.
func runSlashCommand(env *commandEnv, line string) (handled, quit bool) {
	return slash.Run(&slash.Env{
		Svc:        env.svc,
		SessionID:  func() string { return env.info.ID },
		Workspace:  func() string { return env.workspace },
		SetSession: func(info agent.SessionInfo) { *env.info = info },
		Out: func(msg string) {
			if msg != "" {
				fmt.Fprintln(os.Stderr, msg)
				env.out(msg)
			}
		},
	}, line)
}
