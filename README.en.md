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
