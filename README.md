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
- **终端三形态**：`zgo -p "任务"` print 模式、行式 REPL（/compact /plan
  /new）、`zgo -t` 全屏 TUI（ANSI 备用屏 + 流式刷新）。
- **多文件补丁**：apply_patch 工具（Add/Update/Delete，上下文唯一匹配）。
- **长期记忆**：workspace 级记忆跨会话存活，自动注入系统提示。
- **子代理与工作流**：task 工具派发只读研究；workflow 工具编排多步计划，
  edit 步骤继承权限门与会话规则。
- **MCP**：settings 配置 stdio MCP 服务器，其工具以 mcp_ 前缀接入。
- **三协议**：Anthropic 兼容 / OpenAI / Gemini，设置里一键切换。
- **订阅登录**：`/login`（Claude Pro/Max OAuth，PKCE + 本地回调 + 自动刷新），
  三端可用；无 API Key 时以订阅身份调用。
- **插话与并行**：运行中插话（steering）注入下一回合；单批工具调用并行执行
  （同路径读改写自动串行化），子代理与 DAG 工作流并行扇出。
- **Hook**：PreToolUse/PostToolUse 内部钩子 + 设置文件里的外部命令钩子。
- **SSH 远程工作区**：会话目标 `ssh://user@host:port/path`，read/write/edit/
  bash/grep/glob/ls/patch 全量经 SSH 客户端在远端执行，同样的 path jail。
- **工作区检查点与回滚**：git 工作区每回合前快照（临时索引，不触碰真实
  索引），「回滚最近一回合」/编辑重发/重试同时恢复转录与文件（分支与历史
  不动）。
- **diff 全链路**：edit/write/patch 的工具块彩色展示统一 diff；权限确认弹窗
  **批准前预览**待执行改动（试算不落盘）；patch 多文件按段聚合。
- **多模态输入**：composer 📎 添加图片（三协议 base64/dataURL/inlineData）；
  消息内 `@路径` 引用自动展开文件内容（三端同享，path jail）。
- **定时自动化**：`zgo cron` 管理定时任务（五字段 cron，jobs.json 热重载，
  分钟调度器，防重叠），桌面「定时任务」面板可视管理；桌面应用随启动开。
- **终端与文件树**：桌面底部抽屉终端（工作区 shell，Ghostty vt 内核）；
  右列文件树 + 只读预览（本地与 SSH 远程同源）。
- **浏览器目标**：`go tool mygo build -platform web && go run ./cmd/webfs`——
  完整 agent 跑进浏览器（内存 FS 垫片 + IndexedDB 持久化，刷新不清空）。
- **会话导出即分享**：HTML / Markdown 自包含导出（无上传服务——个人 agent
  的分享物就是导出文件本身）。

## 快速开始

只依赖 Go 1.27+：

```bash
go tool mygo dev      # 开发模式（热重载）
go test ./...         # 全部测试（含无窗口视图测试）
go tool mygo build    # 打包（.exe 安装器 / .dmg / .deb）
go run ./cmd/zgo -p "解释这个仓库"   # 终端模式
```

更多形态：

```bash
zgo cron add --schedule '*/10 * * * *' --prompt '看看构建状态' --workspace .   # 定时任务
zgo cron list && zgo cron run <id>                                             # 查看 / 立即执行
go tool mygo build -platform web && go run ./cmd/webfs   # 浏览器版（wasm 静态站点）
```

首次启动后在设置弹窗填入：

| 配置     | 示例                              | 说明                     |
| -------- | --------------------------------- | ------------------------ |
| Base URL | `https://api.anthropic.com`       | 或 GLM / Kimi 兼容端点   |
| API Key  | `sk-...`                          | 只存本地，不回显         |
| Model    | `claude-sonnet-4-5` / `glm-5.2` …| 端点支持的任意模型 ID    |

也可用环境变量：`ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`、`ZGO_MODEL`、
`ZGO_THINKING`（env 优先于设置文件）。

### 项目权限规则

在工作区根放 `.zgorules.json`，权限规则随仓库走（团队成员共享）：

```json
{
  "version": 1,
  "rules": [
    {"tool": "bash", "args": "git *", "behavior": "allow"},
    {"tool": "write", "behavior": "ask"},
    {"tool": "bash", "args": "rm -rf *", "behavior": "deny"}
  ]
}
```

规则按序匹配；`args` 是通配模式（`*` 任意），可写 `"command: git *"`
的规范形态，或 `"git *"` 直接匹配命令本体。解析失败降级为空规则并记
日志，绝不阻塞会话。

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
