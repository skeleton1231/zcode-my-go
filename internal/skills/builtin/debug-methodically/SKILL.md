---
name: debug-methodically
description: Root-cause a bug before touching code — reproduce it, read the actual error, form one hypothesis at a time, and instrument instead of guessing.
when_to_use: Any bug, flaky test, unexpected behavior, or crash investigation — before writing a fix.
---

# Debugging methodically

## Before the first edit

1. **Reproduce.** A bug you cannot trigger on demand you cannot verify a
   fix for. Find the smallest input, the narrowest `-run` filter, the
   exact sequence. If it is intermittent, characterize the window first
   (what varies between passing and failing runs?).
2. **Read the actual error.** Full message, full stack, from the top.
   Error text usually names the file and the violated expectation. Do not
   paraphrase it from memory.
3. **State the mechanism as a hypothesis**: "X writes after Y closes Z,
   so the rename fails." One sentence, falsifiable.

## Then, one move at a time

- Pick the single cheapest observation that would confirm or kill the
  hypothesis (a log line, a breakpoint-equivalent print, a narrower test).
- Observe. Update or discard the hypothesis. Repeat.
- Never change two things between observations — you will not know which
  one mattered.
- If three hypotheses die, stop patching and re-read the component's
  design docs/spec: the mental model is wrong, not the code.

## Anti-patterns

- Editing code to match a guess, then running the full suite as the only
  observation. Full suites answer "does everything pass", not "why did
  this fail".
- Fixing the symptom: catching the error, widening a timeout, retrying
  the flake, deleting the assertion. These relocate bugs, they do not
  close them.
- Trusting names. `Close` that does not wait, `Safe` that races, comments
  describing behavior the code dropped two refactors ago. Read the code
  that runs, not its label.

## Concurrency specifically

- `go test -race` on the narrowed test is the fastest discriminator.
- Goroutine stacks in a hung test's output name the deadlocked frames.
- For "goroutine outlives the test" flakes: find who waits for it at
  teardown. Nobody waiting is the bug.

## Close-out

A fix is done when: the repro passes, the mechanism is explained in one
sentence, and the regression test fails without the fix. Record the root
cause in the commit message — the next reader is future you.
