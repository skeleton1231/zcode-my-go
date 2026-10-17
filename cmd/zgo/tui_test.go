package main

import (
	"os"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

func TestTuiEventColors(t *testing.T) {
	defer func(enabled bool) { colorEnabled = enabled }(colorEnabled)

	colorEnabled = true
	s := &tuiState{width: 120}
	s.applyEvent(agent.Event{Kind: agent.EvMessageStart}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvTextDelta, Text: "hi"}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvToolExecutionStart, ToolName: "read", Args: "{}"}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvToolExecutionEnd, IsError: true, ResultContent: "x"}, new(string))
	s.applyEvent(agent.Event{Kind: agent.EvMessageEnd, StopReason: "error", ErrorMessage: "boom"}, new(string))
	joined := strings.Join(s.lines, "\n")
	for _, want := range []string{"\x1b[1m", "\x1b[90m", "\x1b[31m"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}

	// NO_COLOR：禁色后无转义。
	colorEnabled = false
	s2 := &tuiState{width: 120}
	s2.applyEvent(agent.Event{Kind: agent.EvMessageStart}, new(string))
	if strings.Contains(s2.lines[0], "\x1b") {
		t.Fatalf("escape leaked with colors off: %q", s2.lines[0])
	}
}

func TestAnsiHelper(t *testing.T) {
	defer func(e bool) { colorEnabled = e; os.Unsetenv("NO_COLOR") }(colorEnabled)
	os.Setenv("NO_COLOR", "1")
	colorEnabled = os.Getenv("NO_COLOR") == ""
	if colorEnabled {
		t.Fatal("NO_COLOR not honored")
	}
	if got := ansi("31", "x"); got != "x" {
		t.Fatalf("plain = %q", got)
	}
	colorEnabled = true
	if got := ansi("31", "x"); got != "\x1b[31mx\x1b[0m" {
		t.Fatalf("colored = %q", got)
	}
}
