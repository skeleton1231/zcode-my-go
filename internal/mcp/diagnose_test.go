package mcp

import (
	"strings"
	"testing"
)

// TestDiagnoseHealthy: the real helper server diagnoses green with its
// tools listed.
func TestDiagnoseHealthy(t *testing.T) {
	rep := Diagnose("helper", helperConfig())
	if !rep.OK || rep.Error != "" {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Tools) == 0 || rep.Tools[0] != "echo" {
		t.Fatalf("tools = %v", rep.Tools)
	}
	if rep.LatencyMS < 0 {
		t.Fatalf("latency = %d", rep.LatencyMS)
	}
	if !strings.Contains(rep.Line(), "✓") {
		t.Fatalf("line = %q", rep.Line())
	}
}

// TestDiagnoseBroken: a nonexistent command fails readably, fast.
func TestDiagnoseBroken(t *testing.T) {
	rep := Diagnose("broken", Config{Command: "definitely-not-a-command-xyz"})
	if rep.OK || rep.Error == "" {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(rep.Line(), "✕") {
		t.Fatalf("line = %q", rep.Line())
	}
}
