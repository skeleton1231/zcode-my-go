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

## v0.9.0 highlights

- gopls semantic tools: definition / references / diagnostics with
  symbol precision (same-named methods, comments and strings do not
  match); edit/write receipts carry immediate diagnostics back to the
  model. A resident daemon speaks LSP JSON-RPC (initialize paid once);
  crashes restart transparently, failures fall back to the gopls CLI.
- Cross-session search index: replay-effective text segments persisted
  with (size, mtime) freshness — queries stop re-parsing every JSONL;
  oversized or corrupt sessions fall back to the streamed scan with
  byte-identical results.
- Secret redaction, closed loop: tool outputs pass a redactor at the
  loop's single choke point (events, wire, JSONL, exports and the search
  index all converge there); exact known keys plus high-confidence
  pattern families. `zgo sessions sanitize` scrubs legacy transcripts
  (dry-run by default, --apply rewrites atomically).
- Custom subagents: `.zgo/agents/*.md` personas; task/dispatch accept an
  agent argument (whitelist narrows the tool surface).
- Built-in skills: skill-creator / go-testing / debug-methodically ship
  embedded in the binary; workspace > user > builtin, same name
  overrides.
- MCP dual transport: stdio and Streamable HTTP (2025-03 spec) for
  remote servers; startup no longer waits out handshakes.
- Provider profiles: /provider switches named API profiles (multi-
  account / multi-endpoint) under one redaction boundary.
- Clipboard paste: Ctrl+V drops screenshots straight into the composer
  (DIB→PNG).
- Stop/Notification hooks: a round-end hook (exit 2 blocks the wrap-up
  and feeds the output back) and a permission-wait notification hook.

## v0.11.0 highlights

- Snapshots everywhere: non-git workspaces and ssh:// remote sessions
  get real checkpoints (whole-tree snapshots in the data dir, capped,
  graceful degradation) — rewind / edit-resend / retry now restore
  files on all three workspace kinds.
- Budget economics: a hard fuse (opt-in) rejects new turns at the
  budget; subagent and workflow nested-loop usage all hits the daily
  ledger; `zgo usage` attributes spend per session and source, and
  `--days=N` charts the trend; pin important sessions to the top.
- Telemetry (rule 91): opt-in span tracing (turn / model / tool /
  compact) exported to daily JSONL or OTLP/HTTP — attributes and errors
  redacted, export failures counted and never block the agent.
- MCP server mode (rule 92): `zgo mcp serve` exposes the tool surface
  over stdio JSON-RPC — read-only four by default (no confirmation
  channel), --all opts into the full set; Zcode/Claude Code/Cursor can
  call zgo's path-jailed tools directly.

## v0.10.0 highlights

- Auto-memory: at close, one small completion distills durable facts
  (user preferences, project conventions, key decisions) into the
  workspace memory; injected in future sessions, deduped daily,
  toggleable.
- Custom tools: `.zgo/tools/*.md` command templates ($ARGUMENTS
  placeholder) become model-callable tools; execution rides the bash
  channel (permission judged on the rendered command). /tools lists the
  live tool surface.
- @dir and @line-range mentions: @dir expands a bounded tree; @file.go:
  120-180 carries only the relevant lines.
- /context: one command shows the next request's composition segments
  (system prompt, memory, skills, tool declarations, transcript).
- Regex search: `zgo sessions search --regex` (RE2, case-sensitive,
  (?i) to ignore).
- Session archive: archive/unarchive is orthogonal to content — list
  hides by default, -c skips; transcripts, search and export unchanged.
- Scheduler hardening: a global concurrency cap (2 concurrent runs;
  due jobs queue rather than skip) and RunNow shares the single-flight
  guard.

## v0.8.0 highlights

- Background subagents: the agent tool spawns research agents that
  return an ID immediately (the parent keeps working); agent_output
  harvests, agent_stop kills, agent_send resumes a finished agent with
  its full history. Visible everywhere via /agents, the TUI panel and
  the desktop status strip. Agents can escalate
  questions only the user can answer (prefixed with the agent ID);
  the answer flows back into their transcript.
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
  four assertion kinds) replayed against the real stack and gated in
  CI — with paired trust-gate/trust-granted scenarios standing guard
  over the workspace trust boundary. /find adds in-session transcript
  search. /mcp add/remove configures MCP servers without hand-editing
  settings.json; zgo bugreport emits a redacted, self-contained issue
  report; cron jobs pin their own model and off-peak window; remote
  grep fetches 8-way concurrent.

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
