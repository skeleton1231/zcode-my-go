# Zgo

<div align="center"><strong>Zgo — 属于你的开源桌面 Coding Agent</strong></div>
<div align="center">纯 Go · mygo native UI · 零 Webview · 零前端工具链</div>

<p align="center">简体中文 | <a href="README.en.md">English</a></p>

---

Zgo 是一个开源的桌面 coding agent，**纯 Go 单二进制**：

- 基于 [mygo](https://github.com/egoist/mygo) 的 **native UI**（package `ui`，
  Metal / Direct3D 11 / OpenGL 上由 Go 自绘）：无 HTML、无 JavaScript、无
  webview 进程、无 Bun/Node —— 开发与运行都只要 Go。
- **agent 架构源自 pi**（[earendil-works/pi](https://github.com/earendil-works/pi)）：
  provider 抽象、工具调用循环、类型化事件流、截断调用保护。
- **工程规范源自 ZCode**：spec 先行（`specs/`）、架构治理
  （`architecture-policy.yaml`）、领域词汇（`CONTEXT.md`）、UI 设计规范
  （`DESIGN.md`）。

```
native UI (package ui)  ──▶  AgentService  ──▶  agent loop
   纯 Go 自绘视图              设置 · 会话         ├─▶ ai（Anthropic 兼容 SSE）
   win.Update 跨线程           生命周期           └─▶ tools（read/write/edit/bash/grep/glob/ls）
```

## 特性

- **流式对话**：SSE 流式回复，text_delta / 工具调用 / usage 事件经
  `win.Update` 直达自绘界面（Markdown 流式渲染）。
- **工具执行**：读 / 写 / 精确编辑 / shell（Windows 走 PowerShell）/ grep /
  glob / ls，path jail 限制在 workspace 内，输出自动截断。
- **会话持久化**：JSONL 追加式历史，重启恢复。
- **多 Provider**：任何 Anthropic 兼容端点 —— Anthropic 官方、智谱 GLM
  （`https://open.bigmodel.cn/api/anthropic`）、Kimi 等。
- **中止安全**：随时停止，LLM 流与运行中工具一并取消，会话可继续。
- **即时启动**：无 webview 进程，窗口秒开、内存占用小。
- **权限系统**（译自 ZCode permission/）：allow/ask/deny 三态、会话级
  「总是允许」规则、只读工具自动放行；桌面弹窗或终端 stdin 确认。
- **上下文压缩**（译自 ZCode compact/）：token 预算阈值自动摘要 + 尾部
  保留，连续失败熔断，也可 `Ctrl+K` 手动压缩。
- **任务清单与提问**：todo 工具跟踪计划（会话级状态）；ask 工具把只有
  用户能答的问题送达界面。
- **终端模式**：`zgo -p "任务"` 在工作区直接跑（ZCode zcode-cli print
  模式的 Go 等价）。

## 快速开始

只依赖 Go 1.27+：

```bash
go tool mygo dev      # 开发模式（热重载）
go test ./...         # 全部测试（含无窗口视图测试）
go tool mygo build    # 打包（.exe 安装器 / .dmg / .deb）
go run ./cmd/zgo -p "解释这个仓库"   # 终端模式
```

首次启动后在设置弹窗填入：

| 配置     | 示例                              | 说明                     |
| -------- | --------------------------------- | ------------------------ |
| Base URL | `https://api.anthropic.com`       | 或 GLM / Kimi 兼容端点   |
| API Key  | `sk-...`                          | 只存本地，不回显         |
| Model    | `claude-sonnet-4-5` / `glm-5.2` …| 端点支持的任意模型 ID    |

也可用环境变量：`ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`、`ZGO_MODEL`
（env 优先于设置文件）。

## 工程文档

- [`AGENTS.md`](AGENTS.md) — 给 coding agent 的工程规范（分层、验证命令）
- [`specs/`](specs/) — 行为规格，先于代码
- [`architecture-policy.yaml`](architecture-policy.yaml) — 模块依赖治理
- [`CONTEXT.md`](CONTEXT.md) — 领域词汇表
- [`DESIGN.md`](DESIGN.md) — UI 设计规范
- [`docs/architecture.md`](docs/architecture.md) — 架构说明
- [`docs/zcode-port-map.md`](docs/zcode-port-map.md) — ZCode 全模块移植地图

## License

MIT © 2026 skeleton1231
