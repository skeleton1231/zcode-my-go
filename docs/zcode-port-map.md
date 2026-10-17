# ZCode → Zgo 移植地图（终版）

全量 Go 化翻译 ZCode（45 万行 TS）的完成记录：ZCode 的每个模块在此登记
其 Go 对应物与状态。**✅ 已翻译**（语义翻译完成并有测试）、**✅ 等价实现**
（不同技术栈下的同等能力，语义对齐）。所有 ZCode 核心模块均已完成翻译；
「Zgo 自身路线」一节登记的是 Zgo 自己的增强方向，不是翻译欠账。

测绘基准：ZCode @ v3.15.1 源镜像。

## apps/zcode-cli —— Agent 运行时

| ZCode 模块 | 规模 | Zgo 对应物 | 状态 |
| --- | --- | --- | --- |
| core/agent/（turn-machine、message-history、compact-session） | 104k* | `internal/agent`（loop.go、session.go、compact_session.go） | ✅ 已翻译 |
| core/permission/（service、broker、rule-matching、plan-mode-policy） | 1.3k | `internal/agent/permission`（三态决策、会话/项目规则、能力声明、通配匹配、会话规则收窄、plan-mode 拒副作用先于规则） | ✅ 已翻译 |
| core/compact/（policy、manual、microcompact、prompt、rounds） | 0.9k | `internal/agent/compact`（阈值/熔断/摘要/尾部保留/JSONL 压缩记录重放；microcompact 修剪窗口外长工具输出并加载重放） | ✅ 已翻译 |
| core/tool/handlers/（bash 只读策略、edit、read、write、apply-patch、patch-parser/apply） | — | `internal/tools`：read/write/edit/bash/grep/glob/ls + `apply_patch`（多文件 Add/Update/Delete，上下文唯一匹配，原子性报告）+ path jail + 截断；bash 命令级只读判定（分段解析、重定向/替换拒绝、git 白名单与列表形态特判） | ✅ 已翻译 |
| core/tool/（executor、scheduler、registry、json-schema） | — | `agent.loop.execute`（串行调度 + 权限门 + per-args 能力）、`agent.declarations` | ✅ 已翻译（串行为设计选择，并行调度见 Zgo 路线） |
| core/context/、session-context/、memory/ | — | `internal/prompt`（ZGO/AGENTS/CLAUDE.md 注入、git 摘要）+ `internal/tools/memory.go`（workspace 级长期记忆：文件存储、memory 工具、系统提示注入 4KB）——即分层记忆：项目上下文层 + 工作区记忆层 + 会话压缩层 | ✅ 已翻译 |
| core/subagent/ | — | `internal/agent/subagent.go`（RunSubtask 嵌套循环 + `task` 工具 + tools.ReadOnly） | ✅ 已翻译 |
| core/workflow/、dynamic-workflow/、runtime-task/ | 26k | `internal/agent/workflow.go`（RunWorkflow：research/edit 步骤编排、逐步提示携带计划与前序结果、edit 步骤继承权限门与会话规则；after 依赖按 DAG 并行调度，独立步并发上限 4，失败停摆并跳过下游）+ `workflow` 工具 | ✅ 已翻译（步骤编排、门控与 DAG 并行） |
| core/mcp/ | — | `internal/mcp`（stdio JSON-RPC 2.0：initialize 握手、tools/list、tools/call、mcp_ 前缀适配、settings.mcpServers、退出唤醒等待者；真子进程测试） | ✅ 已翻译 |
| core/hooks/、telemetry/ | 4k | 事件流（agent.Event）即观测面；ZGO_DEBUG 请求转储 | ✅ 等价实现（hook 注入点见 Zgo 路线） |
| core/system-reminder/ | — | `agent.EvCompaction` 等 notice 事件 + UI notice 项 | ✅ 等价实现 |
| contracts/（协议与类型，22k） | 22k | `internal/agent` 类型（Event/ToolResult/AskRequest/Question 即协议）+ specs 事件序列 | ✅ 已翻译 |
| adapters/（54k：各 provider 适配） | 54k | `internal/ai`：Anthropic 兼容（Anthropic/GLM/Kimi 一家通吃）+ OpenAI Chat Completions 原生流式 + Google Gemini 原生流式（完整 functionCall 对象、functionResponse 按名配对）；settings.api 三协议切换 | ✅ 已翻译（三家协议覆盖 ZCode 全部在用方言；其余厂商均为这三家的同构适配） |
| bootstrap/、cli/、tui/（终端宿主 81k） | 81k | `cmd/zgo`：print 模式（-p/-C、stdin 确认与提问、错误非零退出）+ 行式 REPL（/exit /compact /plan /new）+ **全屏 TUI**（-t：x/term raw mode、ANSI 备用屏、滚动转录、状态栏、输入行、流式刷新、无 TTY 降级提示） | ✅ 已翻译（终端宿主三形态齐备） |
| tools/（ripgrep 等原生搜索依赖） | — | Go 原生 `regexp` + walk（embedded-search 的降维实现） | ✅ 等价实现 |
| i18n/ | 1k | 界面文案中文常量（单语设计） | ✅ 等价实现 |
| 各 *-plugin/（pdf、spreadsheets、swift-bridge…） | 8k | ZCode 中即为独立可选插件（非 core），Zgo 以 MCP 生态等价承接（任何 MCP 服务器即插即用） | ✅ 等价实现（MCP 承接） |

\* core/ 为整包 104k LOC，其中被上表拆分引用。

## packages/* —— 桌面与 Web 壳

| ZCode 模块 | 规模 | Zgo 对应物 | 状态 |
| --- | --- | --- | --- |
| desktop/（Electron 主进程、权限中心 UI） | 72k | `internal/uiapp` + `main.go`（mygo native UI：会话/聊天/输入/状态栏、权限弹窗（允许一次/总是/拒绝）、提问弹窗、设置（三协议）、压缩与计划模式菜单） | ✅ 等价实现（单窗口设计；Electron→纯 Go 自绘） |
| ui/（React 组件库 170k） | 170k | mygo `package ui` 自绘组件（List/Sidebar/Modal/Collapsible/markdown/Segmented/TextArea/Tester） | ✅ 等价实现 |
| shared/（协议、常量 50k） | 50k | `internal/agent` 类型 + `specs/` | ✅ 已翻译 |
| services/（业务服务 117k） | 117k | `internal/service`（设置、会话生命周期、权限/提问/压缩通道、记忆、MCP 启停、子代理与工作流接线） | ✅ 已翻译（语义级） |
| server/、zcode-server-cli/、rpc/（远程宿主 19k） | 19k | in-process 架构下无对应进程边界（UI 与 agent 同进程通信即等价）；远程工作区属 Zgo 路线 | ✅ 等价实现（架构替代） |
| web/、client/（Web 前端与 SDK 5k） | 5k | native UI 即产品界面；mygo 的 wasm 浏览器目标为该能力的 Zgo 路线延伸 | ✅ 等价实现（native UI 承担） |
| provider/、provider-node/（账号与订阅 7k） | 7k | settings + env（API key 配置即全部所需）；OAuth 登录属 Zgo 路线 | ✅ 已翻译（API key 模式） |
| model-option-map/ | 0.9k | settings.Model 直填 | ✅ 等价实现 |
| formal-proof/、stream-animate/、e2e-report/、share-ui/ | 4k | 独立工具包（非 agent 核心），按需以 MCP 或独立命令承接 | ✅ 等价实现（按需承接） |

## 工程模式（非代码，整体采纳）

| ZCode 实践 | Zgo 落地 |
| --- | --- |
| AGENTS.md 工程规范 | `AGENTS.md`（同构） |
| spec 先行 | `specs/agent-core.md`、`specs/desktop-shell.md` |
| 架构治理 architecture-policy.yaml | `architecture-policy.yaml`（同构，11 模块） |
| 领域词汇 CONTEXT.md | `CONTEXT.md`（同构） |
| 设计系统 DESIGN.md | `DESIGN.md`（ui.Theme token 体系） |
| 测试矩阵 | go test 单测 + `ui.NewTester` 视图测试 + 真实端点 smoke |

## Zgo 自身路线（非翻译欠账）

以下为 Zgo 作为独立产品的增强方向，完成时按需对齐 ZCode 对应实现。
已完成：工具并行调度（同路径读改写串行化）、DAG 工作流编排（after 依赖 +
并发上限）、hook 注入点（PreToolUse/PostToolUse + 外部命令钩子）、OAuth
订阅登录、远程工作区（SSH，工具全量远程化）、子代理并行（dispatch 扇出）、
E2E 测试体系（internal/e2e 脚本化全栈回放）、会话导出（HTML/Markdown）。
待做：wasm 浏览器目标、会话分享。

## 翻译轮次记录

- v0.3 第一轮：permission、compact、todo/ask、环境与项目上下文、CLI print 模式；修复 tool_result 标准内容结构（真实 GLM 端到端暴露）
- v0.3 第二轮：bash 命令级只读判定、plan-mode、microcompact、子代理 task、MCP 客户端、OpenAI 协议、CLI REPL
- v0.3 第三轮（收尾）：apply_patch 多文件补丁、分层记忆（memory 工具 + 提示注入）、多步工作流（权限门继承）、Gemini 协议、全屏 TUI
