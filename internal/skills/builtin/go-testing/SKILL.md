---
name: go-testing
description: Verify Go changes the way this codebase expects — narrow package test first, then the full gate, table-driven tests, and self-managed temp directories for tests that spawn processes.
when_to_use: Writing or running Go tests, chasing a test failure, or preparing a change for commit/PR.
---

# Testing Go changes

## Order of verification

1. `go build ./...` — compile everything before reasoning about behavior.
2. `go vet ./...`.
3. Narrow run: `go test -count=1 ./internal/<pkg>/ -run TestName -v` while
   iterating. Fast feedback beats full-suite cycles.
4. Full gate before pushing: `go test -count=1 -timeout 600s ./...`.
5. Anything touching goroutines, background writes, or process lifecycles:
   run the full gate **twice**. Tail races (temp-dir cleanup, late writes)
   surface only on the second pass.

## Writing tests

- Table-driven: one `tests := []struct{...}` slice, loop with `t.Run`
  subtests. Name cases by behavior, not input shape.
- Arrange-Act-Assert. One behavior per test function.
- Assert with the whole observable outcome; golden strings beat scattered
  field checks for rendered output.
- Failure messages must let a reader reproduce: include input, want, got.

## Temp directories and processes

- `t.TempDir()` cleanup is one-shot and zero-tolerance to late writers.
  If the test spawns processes (or background goroutines that write files),
  self-manage the directory instead: create under `os.TempDir()`, retry
  removal for a few seconds, and never fail the test on cleanup.
- Kill process trees, not leaves. On Windows the grandchild of a killed
  process survives unless you kill the job/tree.
- Give every `go func` a WaitGroup the owner waits on at teardown. A test
  that ends with live goroutines writing files is a flake in waiting.

## Flakes

A test that fails "sometimes" is a real bug in the test or the code —
triage it the same turn it appears: rerun narrowed, read the goroutine
stack in the -v output, and fix the root cause. Never add sleeps to make
it pass; sleep-based synchronization is a deferred flake.

## Honest reporting

Report what actually ran. If you skipped the full gate, say so. CI
failure logs name the exact assertion — read them before theorizing.
