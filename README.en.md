# Zgo

<div align="center"><strong>Zgo — your own open-source desktop coding agent</strong></div>
<div align="center">Pure Go · mygo native UI · zero webview · zero frontend toolchain</div>

<p align="center"><a href="README.md">简体中文</a> | English</p>

---

Zgo is an open-source desktop coding agent in **one pure Go binary**:

- Built on [mygo](https://github.com/egoist/mygo)'s **native UI**
  (package `ui`, drawn by Go itself on Metal / Direct3D 11 / OpenGL): no
  HTML, no JavaScript, no webview process, no Bun/Node — development and
  runtime need Go alone.
- **Agent architecture from pi** ([earendil-works/pi](https://github.com/earendil-works/pi)):
  provider abstraction, tool-calling loop, typed event stream,
  truncated-call protection.
- **Engineering model from ZCode**: spec-first (`specs/`), architecture
  governance (`architecture-policy.yaml`), a domain glossary (`CONTEXT.md`)
  and a UI design spec (`DESIGN.md`).

```
native UI (package ui) ──▶ AgentService ──▶ agent loop
  pure-Go views             settings ·     ├─▶ ai (Anthropic-compatible SSE)
  win.Update threading      sessions       └─▶ tools (read/write/edit/bash/grep/glob/ls)
```

## Features

- **Streaming chat**: SSE replies with text_delta / tool_call / usage events
  applied to the drawn interface through `win.Update` (streaming Markdown).
- **Tool execution**: read / write / exact-match edit / shell (PowerShell on
  Windows) / grep / glob / ls — path-jailed to the workspace, outputs capped.
- **Persistent sessions**: append-only JSONL transcripts, restored on restart.
- **Any Anthropic-compatible endpoint**: Anthropic, GLM
  (`https://open.bigmodel.cn/api/anthropic`), Kimi, …
- **Safe abort**: Stop cancels the LLM stream and running tools; the session
  stays usable.
- **Instant startup**: no webview process, small memory footprint.

## Getting started

Requires Go 1.27+ and nothing else.

```bash
go tool mygo dev      # develop with live reload
go test ./...         # all tests, including windowless view tests
go tool mygo build    # package (.exe installer / .dmg / .deb)
```

On first launch open Settings and configure Base URL / API Key / Model, or
use `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `ZGO_MODEL` (env beats the
settings file).

## Engineering docs

- [`AGENTS.md`](AGENTS.md) — engineering rules for coding agents (in Chinese)
- [`specs/`](specs/) — behavior specs, written before code
- [`architecture-policy.yaml`](architecture-policy.yaml) — module governance
- [`docs/architecture.md`](docs/architecture.md) — architecture notes

## License

MIT © 2026 skeleton1231

## v0.8.0 highlights

- Background subagents: the agent tool spawns research agents that
  return an ID immediately (the parent keeps working); agent_output
  harvests, agent_stop kills, agent_send resumes a finished agent with
  its full history. Visible everywhere via /agents, the TUI panel and
  the desktop status strip.
- Workspace trust gate: untrusted local workspaces no longer load
  their .zgorules.json (allow rules are a privilege-escalation
  surface) or their .zgo commands/skills — a cloned malicious repo
  can no longer grant itself permissions. /trust reloads rules live;
  `zgo trust` manages the list; the desktop asks on first open.
- Skills: SKILL.md instruction packs with frontmatter (workspace
  overrides global, SSH-native); progressive-disclosure injection into
  the system prompt and a skill tool that loads by name.
- webfetch: fetch URLs and extract readable text — per-hop SSRF
  egress guard (private/loopback ranges all rejected), same-host
  redirect following (cross-host stops and reports), 10MB/100K caps,
  15-min cache; curated doc sites auto-allowed, other hosts ask.
- Workflow persistence: saveAs/run/resume over durable definitions
  and run records — resume skips completed steps by injecting their
  recorded results (drift-guarded).
- Model failover chain: settings.fallbackModels — on retryable
  failures (429/overload) the request re-issues on the next model;
  cron jobs can pin their own model.
- read gains PDFs (text-layer extraction with a readability gate) and
  images (returned as image blocks across all three protocols).
- zgo eval: data-driven JSON scenarios (seed files, scripted turns,
  four assertion kinds) replayed against the real stack; /find adds
  in-session transcript search. /mcp add/remove configures MCP
  servers without hand-editing settings.json; zgo bugreport emits a
  redacted, self-contained issue report.

## v0.7.0 highlights

- Dual-platform CI (GitHub Actions: windows/ubuntu build/vet/test,
  wasm cross-compile, Linux -race) — its first run caught five real
  cross-platform defects.
- REPL raw-mode line editor (insert/backspace by rune, arrows,
  Home/End, Ctrl+C) plus up/down input history in both TUI and REPL
  (dedup, draft restore, FIFO cap).
- Bracketed paste: multiline paste no longer submits per line (REPL
  keeps newlines, TUI folds to spaces); pasted content capped.
- TUI Esc abort: interrupt the running turn without leaving the app.
- Daily token budget guard: a day ledger shared across sessions and
  processes (cache reads priced at 0.1, midnight rollover) with
  80%/100% threshold notices rendered on every surface — soft, never
  blocking. /perms lists the session's always-allow rules.
- MCP trio complete: tools, prompts (/mcp server.prompt), and
  resources (/mcp @server:uri).
- Tool diffs now persist with the transcript (survive reload); HTML
  export gains colored diffs and collapsible thinking, Markdown gains
  diff fences.
- Stability fixes (with community PRs): caps on all unbounded text
  entries, deterministic parallel-batch completion, checkpoint-pruned
  rewind abort, 8.3 short-path handling.

## v0.6.0 highlights


- Background shell over SSH: bash {background:true} runs a non-waiting
  exec channel on ssh:// workspaces (KillTree closes the channel).
- Terminal session resume: -c/--continue (latest in workspace) and
  -r/--resume <prefix>; /resume switches in place with transcript
  replay and nav anchors.
- Terminal steering: messages submitted mid-run queue into the next
  turn (Steer) — parity with the desktop.
- Print automation: --output-format json (one JSONL line per event,
  stable field names) and stdin piping (`git diff | zgo -p "review"`).
- Hierarchical project context: ~/.zgo/ZGO.md user global + one
  context file per level up to the git top (remote workspaces read
  their root over the wire).
- Session auto-title (small completion, equality-guarded; manual
  renames always win), session lifecycle hooks (sessionStart /
  sessionEnd observers, preCompact blocking), usage visibility
  (/usage and `zgo usage` with cache hit rate), exports embed image
  attachments as data URIs, and an e2e suite for ssh:// sessions.

## v0.5.1 highlights


- Rewind to any turn: RewindTo(n) drops turns n..latest (checkpoint
  restore + transcript truncation); desktop offers "⟲ rewind to here"
  on every user turn, terminals get `/rewind [n]`.
- `/export [md] [path]` brings session export to the REPL/TUI/print.
- TUI task surface: pinned todo plan block (refreshed on every todo
  write, folded past six rows) plus Ctrl+Up/Down turn navigation and
  PageUp/PageDown half-screen scrolling, mirroring desktop semantics.
- Desktop turn navigation: Ctrl+Up/Down jumps between user turns
  (stops tail-follow, centers the target, walks search matches).
- `zgo sessions search` — the CLI face of cross-session search.
- Stability: OAuth refresh single-flight (in-lock re-read) and a
  stable system-prompt cache prefix (volatile tail split out).

## v0.5.0 highlights


- SSH terminal: the terminal panel runs a remote interactive shell on
  ssh:// workspaces (PTY + WindowChange).
- Terminal attachments: `-i/--image` in print mode, `/attach` in the
  REPL/TUI.
- Cross-session search: one box over every transcript (text and tool
  arguments), click-through to the session.
- Lazy session loading: startup reads meta lines only (~9ms vs ~2s for
  40 sessions of 320KB).
- Anthropic prompt caching: three ephemeral breakpoints on stable
  prefixes.
- The CLI ops family: zgo cron / doctor / sessions / memory / models.

## v0.4.0 highlights

- Workspace checkpoints & file rewind (git temp-index snapshots; edit-
  resend / retry / rewind restore transcript and files).
- Diffs everywhere: colored unified diffs on edit/write/patch tool
  blocks, approval-time previews (trial-computed, nothing written).
- Multimodal input: image attachments (three wire dialects) and
  @path mentions expanded at the service layer across all surfaces.
- Scheduled automation: `zgo cron` + desktop jobs panel, hot-reloaded
  jobs.json, overlap-safe minute scheduler.
- Terminal panel (workspace shell, Ghostty vt core) and file tree with
  read-only preview — local and SSH workspaces alike.
- Browser target: `mygo build -platform web` + in-memory FS shim with
  IndexedDB persistence — the whole agent in a page, reload-safe.
- Session sharing = self-contained HTML/Markdown export.
