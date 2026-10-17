# ZCode → Zgo 移植地图

全量 Go 化翻译 ZCode（45 万行 TS）的工程化载体：ZCode 的每个模块在这里
登记其 Go 对应物与状态。状态：**已移植**（语义翻译完成并有测试）、
**部分移植**（核心语义在、周边待补）、**规划**（进入路线图）、**不移植**
（明确决定不做，附理由）。参照 ZCode 自身 specs 与源码逐模块核对。

测绘基准：ZCode @ v3.15.1 源镜像（apps/zcode-cli/packages/* 与 packages/*）。

## apps/zcode-cli —— Agent 运行时（本次翻译的主战场）

| ZCode 模块 | 规模 | Zgo 对应物 | 状态 |
| --- | --- | --- | --- |
| core/agent/（turn-machine、message-history、compact-session） | 104k* | `internal/agent`（loop.go、session.go、compact_session.go） | ✅ 已移植 |
| core/permission/（service、broker、rule-matching、plan-mode-policy） | 1.3k | `internal/agent/permission`（三态决策、会话/项目规则、能力声明、通配匹配、会话规则收窄） | ✅ 已移植（plan-mode 语义规划） |
| core/compact/（policy、manual、microcompact、prompt、rounds） | 0.9k | `internal/agent/compact`（window−reserve−buffer 阈值、熔断、摘要提示词、尾部保留、JSONL 压缩记录重放） | ✅ 已移植（microcompact 规划） |
| core/tool/handlers/*（bash 只读策略 40+ 文件、edit、read、write、apply-patch…） | — | `internal/tools`（read/write/edit/bash/grep/glob/ls + path jail + 截断） | 🟡 部分移植（bash 命令级只读判定→权限规则可表达；patch/多文件编辑规划） |
| core/tool/（executor、scheduler、registry、json-schema 校验） | — | `agent.loop.execute`（串行调度）+ `agent.declarations` | 🟡 部分移植（并行调度规划） |
| core/context/、session-context/、memory/ | — | `internal/prompt`（AGENTS/CLAUDE/ZGO.md 注入、git 摘要） | 🟡 部分移植（分层记忆/topic 归档规划） |
| core/subagent/、runtime-task/、workflow/、dynamic-workflow/ | 26k | — | 📐 规划（子代理与工作流在 loop 接口预留位之后） |
| core/mcp/ | — | — | 📐 规划（MCP 客户端：stdio JSON-RPC） |
| core/hooks/、plugin-reference/、telemetry/ | 4k | — | 📐 规划 |
| core/system-reminder/ | — | `agent.EvCompaction` 等 notice 事件 | 🟡 部分移植 |
| contracts/（协议与类型，22k） | 22k | `internal/agent`（Event/ToolResult/AskRequest 类型即协议） | 🟡 部分移植（随模块补齐） |
| adapters/（54k：各 provider 适配） | 54k | `internal/ai`（Anthropic 兼容协议一家通吃 Anthropic/GLM/Kimi） | 🟡 部分移植（OpenAI 原生协议规划） |
| bootstrap/、cli/、tui/（终端宿主 81k） | 81k | `cmd/zgo`（print 模式：-p/-C、stdin 确认与提问） | 🟡 部分移植（全屏 TUI 规划） |
| tools/（ripgrep 等原生搜索依赖） | — | Go 原生 `regexp` + walk（embedded-search 的降维实现） | ✅ 等价实现 |
| i18n/ | 1k | 界面文案中文常量 | 🟡 部分移植 |
| 各 *-plugin/（pdf、spreadsheets、swift-bridge…） | 8k | — | 🚫 不移植（插件生态按需另启） |

\* core/ 为整包 104k LOC，其中被上表拆分引用。

## packages/* —— 桌面与 Web 壳（native UI 路线下的映射）

| ZCode 模块 | 规模 | Zgo 对应物 | 状态 |
| --- | --- | --- | --- |
| desktop/（Electron 主进程、窗口、权限中心 UI） | 72k | `internal/uiapp` + `main.go`（mygo native UI：会话/聊天/输入/状态栏/权限弹窗/提问弹窗/设置） | 🟡 部分移植（多窗口、主题定制规划） |
| ui/（React 组件库 170k） | 170k | mygo `package ui` 自绘组件（List/Sidebar/Modal/Collapsible/markdown） | ✅ 等价实现（自绘替代 React） |
| shared/（协议、常量 50k） | 50k | `internal/agent` 类型 + `specs/` | 🟡 部分移植 |
| services/（业务服务 117k：session、storage、settings…） | 117k | `internal/service`（设置、会话生命周期、Chat 回调、权限/提问通道） | 🟡 部分移植 |
| server/、zcode-server-cli/、rpc/（远程宿主 19k） | 19k | — | 📐 规划（in-process 即等价；远程工作区后置） |
| web/、client/（Web 前端与 SDK 5k） | 5k | — | 🚫 不移植（native UI 路线；浏览器目标走 mygo wasm 能力，另立项） |
| provider/、provider-node/（账号与订阅 7k） | 7k | `internal/service` settings + env | 🟡 部分移植（OAuth 登录规划） |
| model-option-map/ | 0.9k | settings.Model 直填 | ✅ 等价实现 |
| formal-proof/、stream-animate/、e2e-report/、share-ui/ | 4k | — | 🚫 不移植（独立工具，按需另启） |

## 工程模式（非代码，整体采纳）

| ZCode 实践 | Zgo 落地 |
| --- | --- |
| AGENTS.md 工程规范 | `AGENTS.md`（同构） |
| spec 先行 | `specs/agent-core.md`、`specs/desktop-shell.md` |
| 架构治理 architecture-policy.yaml | `architecture-policy.yaml`（同构） |
| 领域词汇 CONTEXT.md | `CONTEXT.md`（同构） |
| 设计系统 DESIGN.md | `DESIGN.md`（ui.Theme token 体系） |
| 测试矩阵（单测 + 视图测试 + e2e） | go test 单测 + `ui.NewTester` 视图测试（e2e 规划） |

## 本阶段新增（v0.3）

- permission 子系统（决策序、规则收窄、gate 集成、桌面/CLI 双通道）
- compact 子系统（阈值/熔断/摘要/持久化重放，手动 + 自动入口）
- 系统提示环境（runtime info、git 摘要、项目上下文文件）
- todo / ask 工具（会话级状态 + 用户提问通道）
- cmd/zgo 终端 print 模式
- 对应测试：permission 3 组、loop gate 5 组、compact 6 组、prompt 4 组、
  todo/ask 5 组、视图弹窗 2 组
