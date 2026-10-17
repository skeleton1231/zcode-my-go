# Zgo

<div align="center"><strong>Zgo — 属于你的开源桌面 Coding Agent</strong></div>
<div align="center">Go 后端 · mygo 桌面壳 · React 前端 · Anthropic 兼容协议</div>

---

Zgo 是一个开源的桌面 coding agent：

- **一个 Go 二进制**：基于 [mygo](https://github.com/egoist/mygo) 桌面框架
  （系统 WebView，无 cgo，跨平台单一可执行文件），内嵌完整 agent 核心。
- **agent 架构源自 pi**（[earendil-works/pi](https://github.com/earendil-works/pi)）
  的分层设计：provider 抽象、双层 agent loop、类型化事件流、工具执行引擎。
- **工程规范源自 ZCode**：spec 先行（`specs/`）、架构治理
  （`architecture-policy.yaml`）、领域词汇（`CONTEXT.md`）、UI 设计规范
  （`DESIGN.md`）。

```
frontend (React)  ──mygo typed IPC──▶  AgentService  ──▶  agent loop
                                                       ├─▶ ai (provider 流式)
                                                       └─▶ tools (read/write/edit/bash/grep/glob/ls)
```

## 特性

- **流式对话**：SSE 流式回复，类型化事件（text_delta / tool_call / usage）直达前端。
- **工具执行**：读 / 写 / 精确编辑 / shell（Windows 走 PowerShell）/ grep / glob / ls，
  path jail 限制在 workspace 内，输出自动截断。
- **会话持久化**：JSONL 追加式历史，重启恢复。
- **多 Provider**：任何 Anthropic 兼容端点 —— Anthropic 官方、智谱 GLM
  （`https://open.bigmodel.cn/api/anthropic`）、Kimi 等。
- **中止安全**：随时 Stop，LLM 流与运行中工具一并取消。

## 快速开始

依赖：Go 1.27+、Bun（仅前端开发）、Node.js 22+。

```bash
# 1. 前端依赖
bun install

# 2. 开发模式（热重载）
bun run dev

# 3. 或直接跑 Go（构建前端后）
bun run --cwd frontend build:web
go run .
```

首次启动后在设置面板填入：

| 配置     | 示例                                        | 说明                       |
| -------- | ------------------------------------------- | -------------------------- |
| Base URL | `https://api.anthropic.com`                 | 或 GLM / Kimi 兼容端点     |
| API Key  | `sk-...`                                    | 只存本地，不进日志与会话   |
| Model    | `claude-sonnet-4-5` / `glm-5.2` …          | 端点支持的任意模型 ID      |

也可用环境变量：`ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`、`ZGO_MODEL`
（env 优先于设置文件）。

## 打包

```bash
bun run build   # 产出安装包（.exe 安装器 / .dmg / .deb，见 mygo 分发文档）
```

## 工程文档

- [`AGENTS.md`](AGENTS.md) — 给 coding agent 的工程规范（分层、验证命令）
- [`specs/`](specs/) — 行为规格，先于代码
- [`architecture-policy.yaml`](architecture-policy.yaml) — 模块依赖治理
- [`CONTEXT.md`](CONTEXT.md) — 领域词汇表
- [`DESIGN.md`](DESIGN.md) — UI 设计规范
- [`docs/architecture.md`](docs/architecture.md) — 架构说明

## License

MIT © 2026 skeleton1231
