# Zgo

<div align="center"><strong>Zgo — your own open-source desktop coding agent</strong></div>
<div align="center">Go backend · mygo desktop shell · React frontend · Anthropic-compatible protocol</div>

---

Zgo is an open-source desktop coding agent:

- **One Go binary**: built on the [mygo](https://github.com/egoist/mygo)
  desktop framework (system WebView, no cgo, single cross-platform
  executable) with the full agent core inside.
- **Agent architecture from pi** ([earendil-works/pi](https://github.com/earendil-works/pi)):
  provider abstraction, tool-calling loop, typed event stream, truncated-call
  protection.
- **Engineering model from ZCode**: spec-first (`specs/`), architecture
  governance (`architecture-policy.yaml`), a domain glossary (`CONTEXT.md`)
  and a UI design spec (`DESIGN.md`).

```
frontend (React)  ──mygo typed IPC──▶  AgentService  ──▶  agent loop
                                                       ├─▶ ai (provider streaming)
                                                       └─▶ tools (read/write/edit/bash/grep/glob/ls)
```

## Features

- **Streaming chat**: SSE responses as typed events (text_delta / tool_call /
  usage) straight to the UI.
- **Tool execution**: read / write / exact-match edit / shell (PowerShell on
  Windows) / grep / glob / ls — path-jailed to the workspace, outputs capped.
- **Persistent sessions**: append-only JSONL transcripts, restored on restart.
- **Any Anthropic-compatible endpoint**: Anthropic, GLM
  (`https://open.bigmodel.cn/api/anthropic`), Kimi, …
- **Safe abort**: Stop cancels the LLM stream and running tools; the session
  stays usable.

## Getting started

Requires Go 1.27+, Bun and Node.js 22+.

```bash
bun install       # frontend dependencies
bun run dev       # desktop app with hot reload
```

On first launch open Settings and configure Base URL / API Key / Model, or
use `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `ZGO_MODEL` (env beats the
settings file).

```bash
bun run verify    # build + vet + go tests + frontend typecheck + vitest
bun run build     # platform packages (.exe installer / .dmg / .deb)
```

## Engineering docs

- [`AGENTS.md`](AGENTS.md) — engineering rules for coding agents (in Chinese)
- [`specs/`](specs/) — behavior specs, written before code
- [`architecture-policy.yaml`](architecture-policy.yaml) — module governance
- [`docs/architecture.md`](docs/architecture.md) — architecture notes

## License

MIT © 2026 skeleton1231
