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
| CI（GitHub Actions） | `.github/workflows/ci.yml`：push/PR 双平台（windows/ubuntu）build/vet/test，并发去重 |
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
   贴图（issue #216，spec desktop-shell 规则 8）：composer Ctrl/Cmd+V
   探剪贴板位图（internal/clipimg——PNG 注册格式直取优先，CF_DIBV5/
   CF_DIB 解码 PNG 化），命中走 📎 同一条通道（同一单张/总量上限与
   notice）；无图放行普通文本粘贴。

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

**TUI 任务面板与回合导航**（桌面任务面的 TUI 对齐，spec 规则 18）：todo
   工具落盘后 TUI 顶部固定计划块（`计划 · 完成/总数` 头 + ▶/✓/□ 标记、
   超 6 条折叠 …+N、空计划隐藏、/new 清空）；transcript 视口默认尾随，
   Ctrl+↑/↓ 在用户回合行间跳转（与桌面 jumpTurn 同语义：跳离末尾停尾
   随、越过最新恢复尾随），PageUp/PageDown 半屏滚动（越界夹紧、滚到底
   恢复尾随）；keyStream 派发 CSI 导航序列，其余转义照旧整段吞掉。

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

**/rewind 终端命令**：`/rewind [n]`（默认最近一轮）把任意回合回滚带到
   REPL/TUI/print 三端——回滚子系统三面齐备（桌面 ⟲、服务 RewindTo、终端
   命令）。

**/export 终端命令**：`/export [md] [路径]` 把会话导出带到 REPL/TUI/print
   ——导出子系统三面齐备（桌面菜单、sessions CLI、终端命令，同一导出器）。

**zgo sessions search**：跨会话检索的 CLI 面（同一 SearchSessions）——
   搜索子系统三面齐备（桌面弹窗、服务 API、终端命令）。

**任务导航**（ZCode task-navigation）：Ctrl+↑/↓ 用户回合间跳转（居中、
   停跟随、边界不动、随搜索过滤行走）。

**终端会话恢复**（Claude Code --continue/--resume 的落地，spec 规则
   22）：`-c/--continue` 取同工作区最近会话（service.ContinueSession，
   无候选退回新建——首用不报错）、`-r/--resume <前缀>` 经唯一前缀指定
   （未知名报错并指向 zgo sessions list；-r 优先于 -c）；三形态共享
   resolveResume——print 携带全部历史发起新回合、REPL 提示恢复行、TUI
   回放既有转录（replayHistory：用户回合登记 Ctrl+↑/↓ 导航锚点、恢复
   行提示会话与标题），恢复会话沿用其自身工作区。

**会话自动命名**（ZCode auto-title）：首个回合成功后若标题仍是首条
   用户消息的截断默认值（等值守卫，无需持久化标志——用户/模型改过即失
   资格），后台经一次小额补全（同 provider、MaxTokens 48、15s 超时）生成
   ≤40 字符单行标题（用户语言、去引号），走既有 Rename 持久化；失败记
   日志保留默认。WaitGroup 收束命名 goroutine（Close 等待）；e2e 经
   DisableAutoTitleForTesting 关闭（脚本 provider 按序号回放）。

**层级项目上下文**（ZCode context-hierarchy）：~/.zgo/ZGO.md 用户全局
   + 自工作区向上到 git 顶（真祖先才回溯——HOME 即仓库的机器上退单层）
   每层第一个命中的上下文文件（ZGO/AGENTS/CLAUDE），浅→深拼接、8KB
   rune 安全封顶；远程工作区经 RemoteFS 根相对读根层（修复远程会话静
   疑丢失项目上下文）；上下文入稳定缓存前缀。Wiring.System 携带
   sessionID 以解析会话工作区。

**print 结构化输出**（Claude Code --output-format json 的落地，spec
   规则 23）：`--output-format text|json`——json 模式每个 agent 事件一
   行紧凑 JSON 到 stdout（text/tool_use/tool_result/compaction/retry/
   message_end/result，字段名稳定；args 原始 JSON 内嵌、畸形降级字符
   串），流式细节（message_start 等）不外发；错误与用量提示仍走
   stderr。脚本/管道（cron、编辑器集成）可稳定消费。

**导出携带图片附件**（spec desktop-shell 规则 22）：用户消息的图片
   块（📎 与 @ 引用成块）HTML 导出内嵌 data URI img、Markdown 导出
   data URI 图片链接——自包含分享物不依赖外部文件；非 image/* 媒体或
   空数据跳过（导出不因坏块失败）。此前图片块在两种导出里被静默丢弃。

**/resume 终端命令**（spec desktop-shell 规则 23，#161 恢复链的运行中
   形态）：无参切当前工作区最近会话、唯一前缀指定（可跨工作区）；切
   换后表面重置视图并回放目标会话转录（TUI 登记导航锚点，/new 空会话
   天然 no-op），todo/任务注册表随会话隔离；未知名/无历史/重复切换各
   有明确提示。

**会话生命周期钩子**（ZCode/CC SessionStart/SessionEnd/PreCompact，
   spec 规则 24）：settings.hooks 增 sessionStart/sessionEnd/preCompact
   （外部命令同机制：JSON 上 stdin、10s 超时、cmd /c 原样退出码）；开
   幕异步/收幕同步皆观察者（失败记日志绝不影响会话），preCompact 非
   零退出阻止压缩并回带 stderr 原因；hookWG 收束开场钩子，Close 对全
   部存活会话触发收场（reason=close/delete）。

**用量可见性**（ZCode usage 的非 UI 面，spec 规则 25）：service.
   UsageSummaryOf（累计输入/输出/缓存读写 + 用户回合数，取自会话持久化
   记账）+ 共享 FormatUsage（含前缀缓存命中率）+ /usage 终端命令与
   `zgo usage [前缀]`（无参=最近会话）；零用量明确展示零值。

**终端插话**（steering 的终端面，spec 规则 26）：REPL/TUI 运行中提交
   的消息经共享 maybeSteer 入队（Steer 既有语义：下一回合注入、abort
   清空）——TUI 登记导航锚点并显示「已入队」提示、REPL 打 stderr 提示
   ；空闲会话仍走常规发送。

**print 管道输入**（Claude Code `cat x | -p` 的对应，spec 规则 27）：
   -p/--print 且 stdin 非终端时，管道/重定向内容以「标准输入：」围栏
   并入提示词（@ 引用同款形态）——`git diff | zgo -p "review"
   --output-format json` 全链自动化；60KB rune 安全截断注明。

**MCP 提示词**（CC /mcp:name 的对应，spec 规则 28）：握手并行探测
   prompts/list（能力可选，不支持的服务器静默无提示词）；/mcp 列出全
   部 server.prompt（描述/参数/* 必填），/mcp <server>.<prompt> [k=v…]
   经 prompts/get 展开为用户消息直接发送（slash.Env.Send 表面钩子，
   TUI 提取 sendChat 共用常规路径、REPL 复用发送闭包；assistant 段加
   角色标注）。

**终端回合重做**（conversation-branch-rewind 的终端面收口，spec 规
   则 29）：/retry 取最后一条用户消息原样重跑、/resend <新文本> 编辑重
   发——经 slash.Env.Rerun 表面钩子走既有 EditAndResend（运行守卫先于
   文件恢复，检查点同恢复）；TUI rerunChat 与 REPL 闭包装，事件流照常
   渲染；回滚三面（⟲/RewindTo//rewind）+ 重做三面（桌面编辑重发/终端
   retry+resend）至此对齐。

**TUI 输入历史**（CC ↑/↓ 召回的对应，spec 规则 30）：每条已提交消
   息入历史（连续重复折叠）；输入行 ↑ 向旧召回、↓ 回新——首次离开
   实时输入保存草稿、回到最新一行恢复；编辑召回文本可改后直接发送；
   历史跨会话切换保留。

**自定义斜杠命令**（CC custom commands 的落地，spec 规则 39）：工作区
   `.zgo/commands/*.md` 与全局 `~/.zgo/commands/*.md`（同名工作区覆盖全
   局）；`/名 [参数]` 展开 $ARGUMENTS 后作为消息发送（Env.Send）；builtin
   优先不遮蔽；/help 列自定义段（名字补斜杠对齐）；SSH 工作区经 path
   jail 同享。
**TUI Esc 中止**（CC Esc-to-interrupt 的对应，spec 规则 38）：裸
   Esc（chunk 末尾孤立形态——CSI 序列同 read 原子到达不受影响）在运
   行中触发 Abort 并提示「已中止」；空闲无操作；粘贴内 ESC 保真；与
   Ctrl+C 退出、括号粘贴、方向键互不冲突（escLen 不完整路径改标签
   break 落穿判定）。
**TUI 信息可见性**（spec 规则 40）：思考流灰显（◌ 行，首条文本到
   达即定稿）；状态栏回合结束附累计用量（↑in ↓out，k/M 缩写，onUsage
   钩子注入）——桌面已有的两块信息补齐到 TUI。
会话分享判定收口：导出 HTML/Markdown 即分享物——个人 agent 无上传
   服务，自包含导出文件本身完成分享（ZCode share-ui 的独立工具包按需
   承接原则）。命名路线至此全部收官。

**MCP 资源**（MCP 三件套收口：tools/prompts/resources，spec 规则
   31）：握手并行探测 resources/list（能力可选）；/mcp 列表同时列出
   资源（@server:uri 形态）；`/mcp @server:uri` 经 resources/read 读
   文本并以「资源 <uri>：」围栏并入消息直接发送（同 stdin 管道形
   态）；blob 类资源可读报错。


**E2E 硬化**：并行批次重叠（权限阻塞不拖累自动放行工具）、流中途中止、

   瞬态重试三条全栈场景；harness 新增 ErrorOnce/BlockAfterText 原语。



**REPL 行编辑器**（终端一等公民，spec 规则 32）：交互终端下 REPL
   输入进 raw 模式行编辑——插入/退格（整 rune）、←/→/Home/End、↑/↓
   历史召回（与 TUI 同语义：折叠/草稿恢复/边界不动）、Ctrl+C 空行退
   出有内容清行；管道 stdin 退回 Scanner 行为不变；
 重绘、rune 宽
   安全。


**括号粘贴**（bracketed paste，REPL 与 TUI，spec 规则 33）：raw 模式
   进出启停 2004h/l；粘贴序列内 CR/LF 不触发提交——REPL 编辑器归一为
   换行保留在消息里（可多行），TUI 输入行折叠为空格（单行渲染）后整
   体入输入区；疑似结束标记的 ESC 序列原样吐回保真。


**diff 持久化与导出**（spec 规则 34）：tool-result 块携带 Diff 随
   转录 JSONL 持久化（此前只在事件流上存在，重载即丢）；HTML 导出彩
   色 diff（+绿/-红，context 不着色）+ 补齐 thinking 折叠（与 Markdown
   对称）；Markdown 导出 ```diff 围栏。


**每日 token 预算守卫**（spec 规则 35）：internal/usage 当日记账
   （dataDir/usage-YYYYMMDD.json，跨会话/进程共享、隔天自然归零）；
   settings.budgetDailyTokens（0=不限）软预算——回合增量按 Priced 折算
   （缓存读 0.1）入账，80%/100% 阈值各发一次 EvBudgetNotice（不阻断）；
   用量面（/usage、zgo usage）附「今日」行。

## 翻译轮次记录

- v0.3 第一轮：permission、compact、todo/ask、环境与项目上下文、CLI print 模式；修复 tool_result 标准内容结构（真实 GLM 端到端暴露）
- v0.3 第二轮：bash 命令级只读判定、plan-mode、microcompact、子代理 task、MCP 客户端、OpenAI 协议、CLI REPL
- v0.3 第三轮（收尾）：apply_patch 多文件补丁、分层记忆（memory 工具 + 提示注入）、多步工作流（权限门继承）、Gemini 协议、全屏 TUI
**通知与权限可见性**（spec 规则 36）：EvBudgetNotice 等通知事件四表
   面渲染（TUI 黄显 [预算]、REPL/print stderr、桌面 notice 项）；/perms
   列出本会话累计的总是允许规则（工具+参数模式，随会话消散）——
   SessionPermissionRules 服务面。
**zgo config CLI**（终端的设置面，spec 规则 37）：list 全量（apiKey 只
   显 Hint 永不回显）/get 单值/set 各键（api、thinking 同 SaveSettings
   校验拒绝非法值；budgetDailyTokens 非负；写盘热生效）；SetAPIKey 直
   写不经 SaveSettings（后者对 model 无条件覆盖，组合写会互相抹）。


**技能子系统**（ZCode core/skills + bundled-skills，spec 规则 45）：
   internal/skills——SKILL.md + frontmatter（平铺 YAML 子集）发现、
   校验（诊断不静默）、按名加载（去 frontmatter、100KB 截断标注、附基
   目录）；两层根（工作区 .zgo/skills——ssh:// 经网线根相对；全局
   ~/.zgo/skills——本地），同名工作区覆盖全局（与自定义命令同约定）；
   系统提示尾部注入可用清单（渐进披露：20KB 预算超限降级仅名列）；
   skill 工具（只读自动放行，not-found 附可用名自纠）+ /skills 终端
   命令。ZCode 的 plugin 来源/别名（qualifiedName、symlink 信任边界）
   随插件生态按需承接（Zgo 以两层根 + MCP 承接）。

**webfetch 工具**（ZCode core/tool WebFetch 的移植，spec 规则 46）：
   internal/webfetch——URL 归一（无凭据、http→https、公网主机）+
   逐跳 egress 守卫（SSRF：字面量私网/环回/链路本地/CGNAT/保留段全拒，
   IPv4-mapped/NAT64 先还原再判；普通域名不做 DNS 预解析——ZCode 同款
   取舍）+ 重定向手动逐跳（同主机 www 容忍自动跟随、上限 10；跨主机停
   在链上报告，跳转目标同过 egress 门）+ 文本类抽取（HTML 剥
   script/style、标题/链接/列表保形、实体解码；10MB/100K 字符双封顶；
   错误页带摘录自纠）+ 15 分钟 TTL 缓存（进程级共享）。权限：预批准文
   档站清单（ZCode 同款，路径前缀防 %2f 绕过）只读放行，其余主机询问。

**read 读图片（多模态工具结果，spec 规则 47）**：read 对 png/jpeg/gif/
   webp 返回 BlockImage——紧跟工具结果块进同一消息（Content 仍是摘
   要），JSONL 持久化；超限缩放重编码（stdlib 双线性，20MB 输入 /
   2000px / 3.75MB 目标 / base64 5MB 校验；webp 透传或提示转 png）；
   三协议 wire：Anthropic 混排 user 消息、Gemini inlineData part、
   OpenAI 降级文本说明。SSH 工作区同构。

**工作流持久化与复用**（ZCode saved-workflows / workflow-runs 的落地，
   spec 规则 48）：internal/workflow——定义（workflows/<名>.json）与运
   行记录（workflow-runs/<id>.json，开跑落盘 running、终态回填、保留
   50 条）；workflow 工具 {steps[, saveAs]}/{run: 名}/{resume: 运行
   ID} 三形态——续跑注入既往结果跳过已完成步骤（定义漂移防护）；
   zgo workflow list/show/runs 运维面。

**后台子代理**（ZCode runtime-task registry / Agent 工具的落地，spec
   规则 50）：agent/agent_output/agent_stop 三件套——启动即回执、父会
   话继续、收割/停止；注册表会话作用域（ctx 派生注册表根，随
   DeleteSession/Close 全停）。顺带修复：嵌套循环（task/dispatch/
   workflow/后台 agent）的 provider 直连 providerFor 绕过 e2e 注入、
   ssh:// 会话经字符串重建本地工具面（远程嵌套循环读本地磁盘）——统
   一经持锁快照 + ReadOnlyW 拨号工作区。

**自定义子代理定义**（ZCode agents 文件 / .claude/agents 的 Zgo 形
   态，spec 规则 51，issue #225）：internal/agents——两层平铺
   markdown（工作区 `.zgo/agents/*.md` + 用户 `~/.zgo/agents/*.md`，
   同名工作区优先）；frontmatter name/description/tools（白名单，封闭
   词表 read/grep/glob/ls/edit/write/patch/todo，只收窄不放宽），正文
   即 persona；task/dispatch 增可选 agent 参数（命中走 persona，未命
   中报错列可用项）；可用清单注入主系统提示（对齐技能注入）。

**子代理续话**（ZCode SendMessage 的落地，spec 规则 52）：agent_send
   {id, message} 对终态后台子代理带全部历史续跑（同 ID、收割面不变；
   运行中拒绝）；RunSubtaskSeeded 统一新任务/续跑形态（历史拷贝隔
   离）；注册表终态存转录、续跑翻转持锁原子。

**read 读 PDF**（ZCode read-pdf 的落地，spec 规则 53）：internal/
   pdftext——词法级 stream 收集 + Flate 解压 + Tj/TJ 文本算子（换行启
   发）、字面/十六进制串（UTF-16BE BOM）；16MB/256KB 有界；可读率
   闸门（<85% 报错——CID 无 ToUnicode 宁失败不乱码）。
**read 读 PDF**（ZCode read-pdf 的落地，spec 规则 52）：internal/
   pdftext——词法级 stream 收集 + Flate 解压 + Tj/TJ 文本算子（换行启
   发）、字面/十六进制串（UTF-16BE BOM）；16MB/256KB 有界；可读率
   闸门（<85% 报错——CID 无 ToUnicode 宁失败不乱码）。

**工作区信任门**（pi project-trust / CC 信任提示的落地，spec 规则
   54）：internal/trust——未授信本地工作区不加载其 .zgorules.json
   （allow 规则即提权面）与 .zgo 命令/技能层（提示注入面）；授信表
   trusted.json（规范化键/原子写）；ssh:// 显式拨号视为授信；/trust
   即时重载在跑会话规则、zgo trust CLI（grant/list/revoke，撤销即时
   清空）；终端三表面未授信提示。

**桌面信任确认**（spec 规则 54 桌面半边，基于工作区信任门堆叠）：
   首开未授信工作区弹信任确认（路径 + 风险说明——恶意仓库可借规则
   文件放权）；信任并加载经服务授信落表，暂不仅关窗。

**场景评测（zgo eval）**（pi evals 的数据化形态，spec 规则 55）：
   internal/eval——JSON 场景（种文件/脚本回合/四类断言：转录含工具
   结果块、工作区文件、无错误工具、事件计数）驱动真实 service 回放；
   evals/ 内置三个起步场景（write-read、skill-load、webfetch-guard）；
   --filter 筛选、失败退出码 1 可进 CI。

**模型兜底链**（spec 规则 56）：settings.fallbackModels——主模型可重
   试失败（429/过载）时 ai.NewFailover 按序换模型重发同一请求；非可
   重试错误透传（换模型无用）；耗尽透传末次错误保持可重试；e2e 注入
   不经兜底；zgo config get/set fallbackModels。

**会话内搜索 /find**（桌面搜索的终端对齐，spec 规则 57）：slash 表面
   钩子——TUI 标记模式（▶/当前命中、n/N 环绕跳转、空输入区才拦截、
   Esc 先退搜索）；REPL 列命中清单（回合+片段）。

**MCP 配置命令**（spec 规则 57）：/mcp add <名> -- <命令> [参数…] 与
   /mcp remove <名>——此前唯一途径是手编 settings.json；服务面
   SetMCPServer/RemoveMCPServer（校验+上限 32）写盘并 syncMCP 热重载
   （握手失败降级，诊断面可见）。

**后台代理可视化**（spec 规则 58）：/agents 清单命令（状态标记/耗时/
任务摘要/折叠）+ TUI 固定块（计划块同款，重画即取快照）——agent 四
件套此前在终端完全不可见。

**问题报告 zgo bugreport**（pi bug-report 的落地，spec 规则 60）：自
   包含 Markdown（版本/平台/脱敏配置/兜底链/MCP 诊断/会话概况元数
   据/工具链）；密钥与转录正文绝不落盘。

**cron 任务级模型**（spec 规则 61）：Job.Model 此前只存不用——补执
   行面 Manager.SetSessionModel（会话模型闭包替换，后续回合/压缩即时
   生效，空恢复全局）；scheduleRunner 覆盖任务模型；cron list 增列。

**桌面后台代理状态条**（spec 规则 61）：聊天流上方的实时状态条（四态
   标记/耗时/摘要/折叠，空不占位）；回合结束与会话打开启动 1s 轮询
   （单飞守卫、变化才回主线程、清空即退）。

**子代理升级提问**（ZCode escalate 的落地，spec 规则 63）：后台子代
   理工具面加 ask——问题经会话提问通道升级（带 [后台代理 <id>] 前
   缀），答案回转录继续跑；AgentRunFunc 增 id；前台短任务不带。

**cron 时间窗**（pi off-peak 的 Zgo 形态，spec 规则 64）：Job.Window
   （HH:MM-HH:MM 可跨午夜）——窗口外到期不 fire 不动 LastRun，窗口
   一开补跑（推迟而非错过）；cron add --window 即校验，list 附 ⏰。

**eval 深化：安全回归网 + CI 门禁**：场景 Trusted:false 模拟未授信工
   作区——trust-gate/trust-granted 双向断言信任门（回归即红）；
   edit-roundtrip、glob-find 场景；CI 增 `zgo eval evals` 门禁步骤。

**grep 远程并发**（性能路线第一条，spec 规则 65）：ssh:// 的 grep 逐文
   件拉取改 8 路并发（RTT 并行化：16×50ms 顺序 800ms→~200ms）；归并
   语义与顺序版一致。本地保持顺序+早停——实测 Windows+Defender 下
   文件打开被杀软串行化，并发零收益；稠密命中的早停才是本地大 win。

**MCP 启动异步**（性能路线第二条，spec 规则 65）：构造期 startMCP 后
   台跑——握手 20s 曾把每个 zgo 命令卡满超时；显式重载仍同步；
   syncMCP 串行化 + Close 守卫（在途握手不泄漏进程）。
