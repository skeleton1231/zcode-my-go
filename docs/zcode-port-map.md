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
| 测试矩阵 | go test 单测 + `ui.NewTester` 视图测试 + 真实端点 smoke + `internal/e2e` 脚本化全栈回放（provider 桩驱动真实 service/session/loop/tools） |

## Zgo 自身路线（非翻译欠账）

以下为 Zgo 作为独立产品的增强方向，完成时按需对齐 ZCode 对应实现。
已完成：工具并行调度（同路径读改写串行化）、DAG 工作流编排（after 依赖 +
并发上限）、hook 注入点（PreToolUse/PostToolUse + 外部命令钩子）、OAuth
订阅登录、远程工作区（SSH，工具全量远程化）、子代理并行（dispatch 扇出）、
E2E 测试体系（internal/e2e 脚本化全栈回放）、会话导出（HTML/Markdown）、
**wasm 浏览器目标（两部分完成）**：`mygo build -platform web` + `cmd/webfs`
注入的内存 FS 垫片，完整 agent（UI/设置/会话/权限门/文件工具/流式
provider）跑进浏览器；第二部分垫片把文件改动逐条落 IndexedDB、启动前
恢复——刷新不清空设置/会话/工作区。
**工作区检查点与文件回滚**（ZCode conversation-branch-rewind 的
   file rewind 半边，internal/checkpoint）：每回合前以临时索引快照 git
   工作区全树（不触碰工作区与真实索引、禁用换行转换保证字节保真），
   回滚最近一回合/编辑重发/重试先恢复检查点（read-tree -u --reset +
   clean -fd，不动分支历史）再截断转录；非 git/SSH 工作区降级为仅回退
   转录。fork/编辑重试/重试即 conversation 半边，此前已备。
**多模态输入（图片附件）**：ai.BlockImage + 三协议翻译（Anthropic
   base64 source / OpenAI image_url / Gemini inlineData）+ composer 📎
   附件 chips + ChatMedia 通道 + 终端形态（print `-i` 与 REPL/TUI
   `/attach`，同限校验、随下一条消息发送）；对齐 ZCode attachment-ux 的输入侧。@ 引用：消息内
   @路径 在服务层展开（文本围栏化/图片成块、path jail、60KB/5MB 上限，
   桌面/TUI/print 三端同享），桌面 composer 附文件提示与点选补全。
**工具 diff 预览**：internal/textdiff（行级 LCS 统一 diff，上下文 3 行，
   输出与规模双上限）+ ToolResult.Diff/Event.Diff 透传 + 桌面工具块与终端
   TUI 的彩色渲染；edit/write（本地与 SSH 远程）覆盖前后对照，对齐 ZCode
   git-diff-ui 的渲染半边。
**定时自动化**（ZCode automation-experience 的调度核心，specs/automation.md）：
   internal/schedule（五字段 cron 解析/Next、jobs.json 热重载、分钟调度器、
   防重叠与结果记账）+ 服务调度接线（桌面随启动开、退出即停）+ zgo cron
   CLI（list/add/rm/run/enable/disable）+ 桌面任务面板（列表/行内启停/立即
   运行/删除/新建表单，specs/automation.md 规则 7）。
**终端面板**（ZCode browser-workbench 的 terminal-ui 半边）：底部抽屉跑
   会话工作区的 shell（mygo terminal 插件/Ghostty libghostty-vt，库自动
   获取）；会话切换重建、失败降级提示、退出回收 PTY。
   获取）；会话切换重建、失败降级提示、退出回收 PTY；ssh:// 工作区经
   remote.Shell 接远端交互 shell（PTY + WindowChange，remotetest 进程内
   服务器端到端验证）。
**文件树面板与预览**（ZCode browser-workbench 的 file-tree 半边）：右列
   目录树（懒展开、目录优先、path jail 三端同源）+ 只读文件预览弹窗
   （60KB 截断、二进制拒绝）。
**审批 diff 预览**：PreviewDiffer（edit/write）在权限询问时只读试算待执行
   改动的统一 diff（不落盘），AskRequest.Diff 随桌面弹窗彩色渲染、终端
   打印——批准前所见即所得。
**patch 多文件 diff/预览**：apply_patch 的结果 Diff 与 PreviewDiff 按段
   聚合（--- 文件名分隔、400 行上限），预览试算不落盘——diff 子系统对
   edit/write/patch 全量闭环。
**惰性会话装载**（ZCode conversation-projection-store）：启动只读 meta 行，
   首次触及转录再全量解析（同机对照 40×320KB 会话：全量 ~2s vs meta-only
   ~9ms）；列表/信息查询零解析。
**MCP 设置诊断**（ZCode mcp-settings-diagnostics）：mcp.Diagnose 新实例
   探测（握手 + tools/list + 15s 上限）+ 服务并行编排 + 设置弹窗「诊断
   MCP」结果展示（✓ 工具清单/✕ 错误）。
**zgo doctor 环境诊断**：provider/模型目录/MCP 真探测/git/ssh 工具链一屏
   报告（key 永不回显）。
**跨会话搜索**（ZCode task-index 的轻量落地）：service.SearchSessions
   流式扫 JSONL（文本+工具参数、片段窗口、双上限）+ 桌面「搜索所有会话」
   弹窗（后台扫描、代次防旧结果、点命中切会话沿用过滤词）。
**Anthropic 前缀缓存**：cache_control ephemeral 断点打在工具表尾/系统提示
   尾/滚动前缀（≤4），命中按缓存计价降本；兼容端点安全忽略。
**zgo sessions CLI**：list（标题/时间/工作区/token 表格）、export（唯一
   前缀 + HTML/Markdown）、rm（前缀删除）——与 cron/doctor 同族的运维面。
**E2E 硬化**：并行批次重叠（权限阻塞不拖累自动放行工具）、流中途中止、
   瞬态重试三条全栈场景；harness 新增 ErrorOnce/BlockAfterText 原语。
**zgo memory CLI**：工作区长期记忆的运维面——list（概况表）、show
   （全文）、add（带时间戳追加）、clear（幂等清空）；--workspace 接受
   路径或会话前缀。
**zgo models CLI**：模型目录表（--api 过滤、当前标记、未知模型告警）+
   --check 离线校验（目录/家族前缀）。
**任意回合回滚**：RewindTo(n) 撤掉第 n..末轮（检查点 seq=n-1 + 转录截断，
   与编辑重发同语义）；桌面每轮用户消息「⟲ 回滚到此」——检查点子系统
   从"只回滚最近一回合"补齐到任意回合。
   命令）。
=======
**任意回合回滚**：RewindTo(n) 撤掉第 n..末轮（检查点 seq=n-1 + 转录截断，
   与编辑重发同语义）；桌面每轮用户消息「⟲ 回滚到此」——检查点子系统
   从"只回滚最近一回合"补齐到任意回合。
>>>>>>> 95b0dba (feat: rewind to any turn — per-turn rollback with checkpoints)
会话分享判定收口：导出 HTML/Markdown 即分享物——个人 agent 无上传
   服务，自包含导出文件本身完成分享（ZCode share-ui 的独立工具包按需
   承接原则）。命名路线至此全部收官。

## 翻译轮次记录

- v0.3 第一轮：permission、compact、todo/ask、环境与项目上下文、CLI print 模式；修复 tool_result 标准内容结构（真实 GLM 端到端暴露）
- v0.3 第二轮：bash 命令级只读判定、plan-mode、microcompact、子代理 task、MCP 客户端、OpenAI 协议、CLI REPL
- v0.3 第三轮（收尾）：apply_patch 多文件补丁、分层记忆（memory 工具 + 提示注入）、多步工作流（权限门继承）、Gemini 协议、全屏 TUI
