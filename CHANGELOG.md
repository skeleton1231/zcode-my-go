# Changelog

约定式提交驱动的里程碑摘要；每一行都对应一个经完整验证与 PR 评审的变更。

## v0.6.0 — 终端一等公民与生命周期

- **后台 shell 远程同构**：bash {background:true} 在 ssh:// 工作区走
  不等待的 exec 通道（KillTree 即关通道），remotetest 流式直写 + 断开
  杀整树（Job Object 确定性）。
- **终端会话恢复**：-c/--continue（同工作区最近）与 -r/--resume <前
  缀>（唯一前缀、可跨工作区）三形态共享；/resume 命令运行中切换并回
  放转录（TUI 登记导航锚点）。
- **终端插话**：REPL/TUI 运行中提交的消息经 Steer 入队（下一回合注入
  不打断当前流），与桌面对齐。
- **print 自动化**：--output-format json（逐事件 JSONL，稳定字段名、
  args 原始内嵌）；stdin 管道并入提示词（`git diff | zgo -p`）。
- **层级项目上下文**：~/.zgo/ZGO.md 用户全局 + 自工作区向 git 顶逐层
  择一（浅→深、8KB rune 封顶）；远程工作区经网线读根层（修复静默丢
  失）。
- **会话自动命名**：首回合成功后小额补全生成 ≤40 字符标题（等值守
  卫，手动改名优先，失败保留默认）。
- **会话生命周期钩子**：sessionStart/sessionEnd（观察者）与
  preCompact（非零阻止压缩），外部命令同机制。
- **用量可见性**：/usage 与 zgo usage [前缀]——累计输入/输出/缓存读
  写 + 回合数 + 前缀缓存命中率。
- **导出携带图片**：用户图片块 HTML 内嵌 data URI img、Markdown 链接
  ——自包含分享物不缺图。
- **E2E 远程工作区套件**：ssh:// 会话全栈四场景（往返/jail/bash/后台
  生命周期）；ParseTarget 还原 Windows 盘符根。
- **稳定性修复**（含社区）：OAuth 刷新单飞、系统缓存前缀稳定、
  isAncestor 卷大小写、checkpoint 修剪后回滚中止、remote KillTree 按
  pid 确认、/attach 预算前置拒绝、e2e 等待预算统一。

## v0.5.1 — 回滚三面与 TUI 任务面

- **任意回合回滚**：RewindTo(n) 撤掉第 n..末轮（检查点 seq=n-1 恢复 +
  转录截断，与编辑重发同语义）；桌面每轮用户消息「⟲ 回滚到此」，
  终端 `/rewind [n]`（默认最近一轮）——回滚子系统三面齐备。
- **/export 终端命令**：`/export [md] [路径]` 三端可用（默认自包含
  HTML），与桌面菜单、`zgo sessions export` 同一导出器。
- **桌面任务导航**：Ctrl+↑/↓ 用户回合间跳转（跳离末尾停跟随、目标居中、
  随搜索过滤行走、切会话重置）。
- **TUI 任务面板与回合导航**：todo 工具落盘后 TUI 顶部固定计划块
  （▶/✓/□ 标记、超 6 条折叠、空计划隐藏、/new 清空）；Ctrl+↑/↓ 回合
  跳转 + PageUp/PageDown 半屏滚动（尾随语义与桌面一致，跨 Read 序列
  拼接识别）。
- **zgo sessions search**：跨会话检索 CLI（同一 SearchSessions，表格
  输出：会话前缀/标题/片段）——搜索子系统三面齐备。
- **稳定性修复**：OAuth 刷新单飞（锁内重读，杜绝并发刷新竞态）；系统
  提示缓存前缀稳定（易变尾段拆出，前缀缓存跨回合命中）。

## v0.5.0 — 深化：远程、性能与 CLI 家族


- **SSH 终端**：终端面板在 ssh:// 工作区直接跑远端交互 shell（PTY +
  WindowChange，进程内服务器端到端验证）。
- **终端附件**：print `-i/--image` 与 REPL/TUI `/attach`——附件四表面齐备。
- **跨会话搜索**：流式扫全部转录（文本+工具参数、片段窗口、双上限），
  桌面弹窗点命中切会话并沿用过滤词。
- **惰性会话装载**：启动只读 meta 行（实测 40×320KB 会话全量 ~2s vs
  ~9ms）；首触转录再合并进壳。
- **Anthropic 前缀缓存**：工具表尾/系统提示尾/滚动前缀三处 ephemeral
  断点（≤4），命中入 CacheReadTokens。
- **MCP 设置诊断**：并行探测全部服务器（新实例握手+tools/list，15s 上限），
  设置弹窗结果展示。
- **CLI 运维家族**：`zgo doctor`（环境诊断）、`zgo sessions`（列表/导出/
  删除，唯一前缀）、`zgo memory`（工作区记忆管理）、`zgo models`（目录表
  + 离线校验）——与 `zgo cron` 齐备。
- **E2E 硬化**：并行批次重叠、流中途中止、瞬态重试全栈场景；harness 新增
  ErrorOnce/BlockAfterText 原语。

## v0.4.0 — workbench 与自动化

- **工作区检查点与文件回滚**：git 临时索引快照，回滚最近一回合/编辑重发/
  重试同时恢复转录与文件（分支与历史不动，字节保真）。
- **diff 全链路**：edit/write/patch 工具块彩色统一 diff；权限确认**批准前
  预览**（试算不落盘）；patch 多文件按段聚合。
- **多模态输入**：图片附件（三协议）+ `@路径` 引用展开（三端同享）。
- **定时自动化**：`internal/schedule` + `zgo cron` + 桌面任务面板；桌面应用
  随启动开调度器。
- **终端面板与文件树**：底部抽屉工作区 shell（Ghostty vt 内核）；右列文件
  树 + 只读预览（本地与 SSH 同源）。
- **审批与并发加固**：权限询问单槽串行化；并行批次同路径读改写串行化；
  remote Exec 并发排空与输出上限；附件消息预算兜底。
- **E2E 测试体系**：`internal/e2e` 脚本化 provider 驱动全栈（工具往返、权限、
  插话、压缩、检查点、附件、@ 引用、定时运行、diff）。
- **浏览器目标**：`mygo build -platform web` + 内存 FS 垫片 + IndexedDB
  持久化——完整 agent 跑进浏览器，刷新不清空。

## v0.3 — ZCode 核心翻译（三轮收官）

- 权限系统（三态/规则/能力声明/plan-mode）、上下文压缩（阈值/熔断/
  microcompact）、todo/ask、bash 命令级只读判定。
- apply_patch 多文件补丁、分层记忆（memory 工具 + 提示注入）、多步工作流
  （权限门继承）、task 子代理、MCP 客户端（stdio）。
- 三协议 provider：Anthropic 兼容（thinking 预算）/ OpenAI / Gemini；
  终端三形态：print / REPL / 全屏 TUI。

## v0.2 — native UI 定型

- React 前端 → mygo 纯 Go 自绘（零 webview、零前端工具链）。
- 会话/聊天/设置/权限弹窗骨架；JSONL 持久化；agent loop 事件协议。

## v0.1 — 初始骨架

- provider 抽象 + Anthropic 兼容流式；read/write/edit/bash 四工具与 path
  jail；桌面壳起步。
