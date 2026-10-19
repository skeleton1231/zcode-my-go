# Spec: agent-core

Status: implemented (v0.1)

## 范围

`internal/ai`、`internal/agent`、`internal/tools` 构成的 agent 核心：provider 抽象、
agent loop、工具执行与会话。桌面壳（`main.go`、`internal/service`、前端）只通过
本 spec 定义的边界消费核心。

## 产品规则

1. Agent 是消息驱动的循环：用户消息进入历史 → LLM 流式回复 → 若含工具调用则执行
   并把结果追加为 toolResult 消息 → 再次调用 LLM，直到回复不含工具调用或被中止。
2. 单轮内工具按序执行（sequential）；接口预留 parallel 扩展位。
3. `stopReason == "length"`（输出被 token 上限截断）时，本轮所有工具调用一律
   不执行，改写为错误 toolResult，告知模型参数可能不完整。
4. 中止（abort）立即取消：LLM 流取消、运行中的工具收到取消的 context，已完成的
   工具结果保留在历史中，会话可继续。
5. 会话 = 有序消息历史 + 元数据。持久化为 JSONL（每行一条记录），追加写入，
   不改写历史。
6. 工具的文件访问被限制在 workspace root 内（path jail）；越界路径返回错误，
   不做重定向。
7. 所有工具输出必须截断到上限（bash 30KB、read/grep 每文件 60KB），超限提示
   截断发生。
9. 工作区检查点（ZCode conversation-branch-rewind 的 file rewind 半边）：
   每个用户回合开始前，若工作区是本地 git 仓库，则以临时索引捕获全树
   （tracked 改动 + 未跟踪文件，不含 ignored）为 refs/zgo-checkpoints/
   <session>/<seq>，不触碰工作区与真实索引；每会话保留最近 20 个。
   回退类操作（回滚最近一回合 / 编辑重发 / 重试）先把工作区恢复到对应
   回合的检查点（read-tree -u --reset + clean -fd，不移动分支、不改历史；
   ignored 文件永不被清除），再截断转录。非 git 工作区与 SSH 远程工作区
   降级为仅回退转录，不报错；git 仓库中检查点已被保留窗口修剪时**不降
   级**——报错中止回退（ErrCheckpointPruned 与 ErrNotARepo 两个哨兵分
   开，issue #152：静默半回退让模型拿旧假设改新文件）。
10. 多模态输入：用户消息可携带 BlockImage（Media 为 MIME 类型，Data 为
   base64，位于文本块之后），随转录持久化；三家协议各自翻译（Anthropic
   base64 source、OpenAI image_url data-URL、Gemini inlineData），纯文本
   消息的 wire 格式不变。附件单张上限 5MB，超限拒绝。
8. 工作流（workflow 工具）：步骤串行执行，每步看到完整计划与此前结果；任一步
   失败即停止，报告已完成部分。步骤声明 `after` 依赖（按 `name` 引用）时按 DAG
   调度：无依赖关系的步骤并行（上限 4），每步只看到其直接上游的结果；首个
   失败后不再派发新步骤，已在跑的步骤跑完，未启动的依赖链在报告中记为跳过；
   出现 `after` 时全部步骤必须有唯一 `name`，引用不存在的名字或成环即拒绝。

9. 工具 diff：edit/write（覆盖）在 ToolResult.Diff 携带统一 diff
   （internal/textdiff：LCS 行级 diff，3 行上下文，输出上限 400 行，超规模
   输入降级为整块替换）；Diff 随 EvToolExecutionEnd 事件透传给 UI 彩色渲染
   （+绿/-红/@@强调），不进模型上下文（Content 仍是给模型的摘要）。

11. @ 引用：消息里的 @路径 在发送前于服务层展开（三端同路径）——图片
   文件变 image 块，文本文件内容以围栏附于消息尾部（动态围栏——按内容
   最长反引号串 +1 加长，issue #178；60KB 截断；图片 5MB
   上限，会话层另有 6MB 总量兜底）；引用名保留在原文，不存在的引用原样
   留给模型；读取统一过 path jail（本地与 SSH 远程同源）。桌面 composer
   对尾部 @token 浮出工作区文件提示，点选补全。
12. 审批预览：实现 PreviewDiffer（edit/write）的工具在权限询问时计算
   待执行改动的统一 diff（只读试算，不落盘），随 AskRequest.Diff 交给
   审批面（桌面弹窗彩色渲染、终端打印）——批准前所见即批准后所得。
13. patch 的 diff：apply_patch 的结果与审批预览按段聚合（--- 文件名 分
   隔），聚合上限 400 行；预览与执行同序，遇无法试算的段即止（与执行的
   首错停止一致）；预览不落盘。
15. 惰性会话装载（ZCode conversation-projection-store 的落地）：启动只
   读每个会话文件的 meta 行（壳含 ID/标题/工作区/mtime），列表与信息查询
   零转录解析；首次触及转录的方法（Messages/Append/lastUserIndex/
   UsageTotal/Chat 微压缩/Rewind）持锁全量装载并把结果合并进壳（wiring
   等运行时字段不动）。首行非 meta 的损坏文件退回全量装载；装载失败置空
   转录并记日志，不重试。新会话天然已装载。
14. 终端附件形态：print 模式 `-i/--image <路径>`（可重复）随提示发送；
  REPL/TUI 的 `/attach <路径>` 攒待发附件（状态栏 📎×N 指示），随下一条
  消息经 ChatMedia 发送后清空；类型/大小校验与桌面 📎 同限（5MB），失败
  逐项提示不阻断发送。总量预算同样提前拒绝（issue #132）：挂载时按编码
  字节计，超 6MB 即拒并提示分条发送（与桌面 attachmentTotalCapBytes
  同口径同文案），不再等发送时的 session 兜底。
16. 前缀缓存（Anthropic 协议）：请求在稳定前缀的末尾打 ephemeral 断点——
   工具表末项、系统提示尾部、倒数第二条消息的最后一块（滚动前缀），总数
   不超过 API 上限 4；空前缀自动少打。兼容端点不识别 cache_control 时按
   未知字段忽略。缓存命中的用量记入 CacheReadTokens。
17. 任意回合回滚：回滚到第 n 轮之前 = 恢复该回合开始前的检查点（seq =
   n-1）并把转录截断到该轮用户消息之前（撤掉第 n..末轮的全部效果——与
   编辑重发对最后一轮的语义一致）。运行守卫先于文件恢复；越界轮次拒绝；
   桌面在每轮用户消息上提供「⟲ 回滚到此」。
18. TUI 任务面板与回合导航（与桌面对齐）：todo 工具每次执行后 TUI 顶部
   固定渲染计划块（`计划 · 完成/总数` 头 + ▶ 进行中/✓ 完成/□ 待办，超
   6 条折叠 …+N），计划为空即整块隐藏；transcript 默认尾随（新行到达视
   口贴底），Ctrl+↑/↓ 在用户回合行间跳转（跳离末尾停尾随、越过最新恢复
   尾随），PageUp/PageDown 半屏滚动，窗口越界夹紧；/new 切会话清空计划
   与回合锚点。
19. 后台 shell 本地与远程同构：bash {background:true} 启动不等待完成的
   命令（cd 工作区根、stdout/stderr 合并）并立即返回 job id；输出流入
   256KB 保留缓冲；bash_output 按调用增量排空（首行 job/命令/运行或退
   出状态，之后新输出按截断上限）；bash_kill 终止整棵进程树——本地走
   平台整树击杀（Windows Job Object，daemon 化也逃不掉），远端关闭
   SSH 会话通道（SIGHUP 覆盖会话进程组）并尽力确证（issue #166：POSIX
   远端经首行标记捕获 pid——标记行吞掉不进输出缓冲——补杀进程组 +
   组长 + 直系子进程；cmd 远端与未捕获 pid 时仅通道关闭，残留可能记
   日志可见；双 fork daemon 挂到 init 名下仍不可达，SSH 没有 Job
   Object，目标是确证 + 可见而非完美对等）；会话结束 KillAll 收尾。
   远程后台与本地后台语义一致（同一 Jobs 注册表与工具面）。
20. 层级项目上下文（ZCode context-hierarchy）：系统提示注入用户全局
   （~/.zgo/ 下 ZGO.md/AGENTS.md/CLAUDE.md 同序择一——issue #217：用户通用偏好常以 AGENTS.md 心智书写）+ 自工作区向上到 git 顶（含）每层第一个命中的上下
   文件（ZGO.md/AGENTS.md/CLAUDE.md，逐层择一），浅→深拼接（一般→具
   体）带层级标注；总量 8KB 封顶（rune 边界截断）并附说明。git 顶必须
   是工作区真祖先（HOME 即仓库的机器上不回溯，issue #98 教训）；非 git
   工作区只读根层。远程工作区经 RemoteFS 根相对读取根层（此前读本地
   路径必然落空——远程会话静默丢失项目上下文）；上下文进稳定块（跨
   轮缓存前缀的一部分）。
21. 会话自动命名（ZCode auto-title）：首个回合成功结束后，若标题仍是
   首条用户消息的截断默认值（等值即守卫——用户或模型改过即失去资
   格，无需持久化标志），后台经一次小额补全调用（同 provider/模型、
   MaxTokens 48、15s 超时）生成 ≤40 字符单行标题（用户语言、无引
   号），经既有 Rename 持久化到 meta；失败记日志保留默认标题，不影响
   会话。手动改名优先于自动命名。
22. 终端会话恢复（Claude Code --continue/--resume 的落地）：`-c/
   --continue` 取同工作区最近修改的会话，`-r/--resume <前缀>` 经唯一
   前缀指定会话（未知名/歧义报错，完整 ID 见 zgo sessions list）；解析
   成功则沿用该会话及其工作区（即使与当前目录不同），print 携带全部
   历史发起新回合，REPL/TUI 回放既有转录后继续（用户回合登记导航锚
   点，恢复行提示会话与标题）；--continue 无候选会话时退回新建（首用
   不报错）。-c 与 -r 同给时 -r 优先。
23. print 结构化输出（Claude Code --output-format 的落地）：
   `--output-format text|json`（默认 text）。json 模式每个 agent 事件一
   行紧凑 JSON 到 stdout（type=text、tool_use、tool_result、
   compaction、retry、message_end、result），字段名稳定（text/tool/
   args/result/is_error/diff/stop_reason/error）；args 为原始 JSON 内嵌
   （非法 JSON 时字符串降级）；错误与用量提示仍走 stderr，文本模式行
   为不变。
24. 会话生命周期钩子（ZCode/CC SessionStart/SessionEnd/PreCompact）：
   settings.hooks 增 sessionStart/sessionEnd/preCompact（外部命令，与工
   具钩子同机制：JSON 上 stdin、10s 超时）。sessionStart 在新建会话后
   异步触发、sessionEnd 在删除与 Close（全部存活会话，reason=delete/
   close）同步触发——两者皆观察者（失败/非零退出记日志，绝不影响会
   话）；preCompact 在压缩前同步触发，非零退出阻止压缩并把 stderr 原
   因回给用户。payload 含 sessionId/workspace/title（压缩另含 messages/
   trigger）。
25. 用量可见性（ZCode usage 面的非 UI 面）：/usage 终端命令与
   `zgo usage [前缀]`（无参 = 最近会话）展示会话累计 token——输入/输
   出/缓存读/缓存写（缓存读说明前缀缓存命中）与用户回合数，取自会话
   持久化的 usage 记账；未产生用量时明确展示零值而非空。桌面状态栏
   等呈现面按需承接，同一数据源。

26. 终端插话（steering 的终端面，与桌面对齐）：REPL/TUI 在回合
   运行中提交的新消息经 Steer 入队（下一回合生效、不打断当前流），转
   录/输出显示「已插话」提示与原消息；插话失败（空消息等）显示原因；
   空闲会话仍走常规发送。插话队列的既有语义不变（abort 清空、下一回
   合前注入）。
27. print 模式管道输入（Claude Code `cat x | -p` 的对应）：-p/--print
   且 stdin 非终端（管道/重定向）时，输入内容以「标准输入：」围栏附在
   提示词尾部（与 @ 引用同款形态——动态围栏按内容最长反引号串 +1 加
   长，issue #178：管道高频输入恰是 Markdown，行首裸 ``` 不得提前闭合）
   一并发送；空输入不变；超限按 rune
   计截断到 60K 字符并注明（与 @ 引用 mentionTextCap 同口径——ASCII
   管道保满额度）。REPL/TUI 不受影响（stdin 是交互端）。

28. MCP 提示词（CC /mcp:name 的对应）：握手后并行探测 prompts/list
   （能力可选——不支持的服务器静默无提示词）；`/mcp` 列出全部
   server.prompt（描述与参数，* 为必填）；`/mcp <server>.<prompt>
   [k=v…]` 经 prompts/get 展开为用户消息直接发送（文本块按角色拼接，
   assistant 段标注）；未知名字给可读报错并列出可用项；不支持直接发
   送的表面明确说明。
29. 终端回合重做（conversation-branch-rewind 的终端面，与桌面对
   齐）：`/retry` 取最后一条用户消息原样重跑（撤当前轮含工作区检查点
   恢复后重发）；`/resend <新文本>` 编辑重发（同语义、替换文本）。两
   者经表面 Rerun 钩子走既有 EditAndResend（运行守卫先于文件恢复）；
   无历史回合/运行中/钩子缺失各有明确提示；事件流按常规渲染。
30. TUI 输入历史（CC 的 ↑/↓ 召回）：每条已提交的用户消息入历史
   （连续重复折叠）；输入行上 ↑ 向旧召回、↓ 向新，回到最新一行恢复
   召回前的草稿；编辑召回文本后回车即发送（不改动历史本体）；边界不
   动。会话切换（/resume//new）不清历史——召回跨会话可用；历史封顶 500 条（超限丢最旧 FIFO，issue #192）。
31. MCP 资源（MCP 三件套收口）：握手并行探测 resources/list（能力
   可选，不支持的服务器静默无资源）；/mcp 列表同时列出资源（server:
   uri 形态，标注描述与媒体类型）；`/mcp @server:uri` 经 resources/read
   读取文本内容并以「资源 <uri>：」围栏并入消息直接发送（同 stdin 管
   道形态——动态围栏按内容最长反引号串 +1 加长，issue #178）；资源文
   本 60K rune 封顶（超限截断 + 原长标注，issue #194）；非文本资源
   （无 text 块）给可读报错；未知服务器/资源报错并列出可用项。

32. REPL 行编辑器（终端一等公民）：交互终端下的 REPL 输入进入
   raw 模式行编辑——字符插入/退格（整 rune）、←/→ 光标移动、Home/
   End 跳首尾、↑/↓ 历史召回（连续重复折叠、首次离开保存草稿、回最新
   恢复、边界不动；历史与 TUI 同封顶 500 条，issue #192）、Enter 提
   交、Ctrl+C 空行退出（有内容清行、连按退出）；管道/重定向（非终端
   stdin）退回 Scanner 行为不变。输出以重绘当前行，中文宽字符按 rune
   处理。

33. 括号粘贴（bracketed paste，REPL 与 TUI）：进入 raw 模式启用
   [?2004h、退出关闭；粘贴序列（[200~…[201~）内的 CR/LF
   不触发提交——REPL 编辑器保留为换行（消息可多行），TUI 输入行折叠
   为空格（单行渲染）后整体入输入区；粘贴外的回车语义不变。粘贴内容
   60K rune 封顶（超限截断 + 原长尾注，issue #195——对齐 stdin 管道
   口径）。

34. diff 持久化与导出：tool-result 块携带 Diff（edit/write/patch
   等改动类工具的统一 diff，随转录 JSONL 持久化——此前只在事件流上
   存在，重载/导出即丢）；HTML 导出以彩色行渲染（+绿/-红）、
   Markdown 以 diff 围栏呈现于工具输出之后（围栏动态加长，issue
   #178：编辑 Markdown 文件时 diff 行自身可含围栏）；HTML 导出同
   时补齐 thinking 块（<details> 折叠，与 Markdown 对称）。

35. 每日 token 预算守卫：settings.budgetDailyTokens（0=不限）；
   每回合的增量用量（含缓存读 0.1 计价）累计到 dataDir 的当日记账
   （usage-YYYYMMDD.json，跨会话共享、进程外持久；进程内检测日期翻滚——跨零点清零计数、切换新文件、重置阈值标志，issue #199：桌面连跑数天是常态）；用量面（/usage
   与 zgo usage）展示「今日」行；跨过阈值 80% 与 100% 各发一次通知
   事件（EvTurnRetry 同级的用户可见提示，不阻断运行——软预算）。

36. 通知与权限可见性：EvBudgetNotice 等用户可见通知事件在四表面
   （TUI/REPL/print/桌面）渲染为提示行（黄显、不进转录）；/perms 列
   出本会话累计的「总是允许」权限规则（工具与路径模式）——会话结束
   随之消散，列表为空时说明用法（终端按 y/a 回答 ask 时 a 会积累规
   则）。68d2967 (feat: notice rendering and session permission visibility)

37. zgo config CLI（终端的设置面，与桌面设置面板同源）：
   `config list` 全量（key 永不回显，Hint 形态）；`config get <键>` 单值；
   `config set <键> <值>`——model/api/baseUrl/thinking（同 SaveSettings
   校验：非法 api/thinking 拒绝）、apiKey（写后同样只回 Hint）、
   budgetDailyTokens（≥0 整数，0=不限）。写盘即热生效（rewire/syncMCP
   同既有路径）；未知键/缺值可读报错。

38. TUI Esc 中止运行（CC 的 Esc-to-interrupt）：回合运行中按裸 Esc
   触发 Abort（当前流截断、转录保留已产出部分、状态回就绪）并提示
   「已中止」；空闲时裸 Esc 无操作（不退出、不清输入）；与 Ctrl+C
   （退出应用）、括号粘贴标记、方向键序列互不冲突。

39. 自定义斜杠命令（CC custom commands 的落地）：工作区 `.zgo/commands/
   *.md` 与全局 `~/.zgo/commands/*.md`（同名工作区覆盖全局）；命令名即文
   件名去扩展，`/名 [参数]` 执行——内容作为提示词发送，`$ARGUMENTS` 占
   位替换为参数原文；builtin 优先，自定义不遮蔽；/help 列出自定义段；
   不可发送的表面展示展开文本。SSH 工作区同享（经 path jail 读取）。
（命令文件文本 60K rune 封顶——issue #195 家族口径。）


40. TUI 信息可见性（与桌面对齐）：思考流灰显——EvThinkingDelta 累积渲染
   为「◌ …」行（首条文本 delta 到达即定稿，zgo 文本流行复位）；状态
   栏在回合结束后携带会话累计用量（↑输入+缓存 ↓输出，k/M 缩写，经
   UsageSummaryOf，零用量时保持「就绪」）。


41. edit 自纠线索：未命中/非唯一的错误消息附带诊断——最近匹配的连续
   行窗口（行号 + 截断摘录，空行不计入相似度信号）、多处命中列出全
   部行号（封顶）、仅空白/缩进差异点名（issue #218：错误消息即自纠
   燃料，免去整文件重读）。摘录与扫描均有封顶，不膨胀错误消息。


42. bash cwd 粘滞（issue #221）：cd 跨调用保持——命令尾部追加目录哨
   兵（shell 回显最终目录，工具侧解析并剥离，不泄漏进输出），下一
   条命令从该目录启动；越出工作区根的 cd 不持久（jail 语义）；环境
   变量不持久（只粘滞目录）；并行批次内共享起点、收尾 last-write
   更新。SSH 远程暂不粘滞（后续跟进）。


43. 检查点随会话清光（issue #224）：DeleteSession 删除转录后调用
   checkpoint.RemoveAll——该会话全部 refs/zgo-checkpoints/<id>/* 逐
   条 update-ref -d（活 ref 钉住 git 对象，不清是硬泄漏）；非仓库/
   ssh:// 静默无操作，失败记日志不阻断删除。
44. glob/grep 感知 .gitignore（issue #208）：遍历前解析工作区根
   .gitignore（经 workspace 抽象，远程同享），实现 git 风格子集——
   注释/空行、! 反选、尾随 / 目录限定、含 / 锚定、* 与 ? 通配；与
   静态 skipDirs 取并集（无 .gitignore 行为不变）；命中目录整枝剪
   除，命中文件逐个跳过；会话级加载一次（.gitignore 变更下轮会话
   生效）。
dc907a4 (feat: glob and grep honor .gitignore)

45. 技能（ZCode skills 子系统）：SKILL.md + frontmatter（平铺 YAML
   子集：name/description 必填、when_to_use 可选、引号值去引号；无
   frontmatter 按目录名收录；描述超 1024 字符拒绝）；两层根——工作
   区 `.zgo/skills`（ssh:// 经网线，根相对形态）与全局
   `~/.zgo/skills`（本地），同名工作区覆盖全局（与自定义命令同约
   定）；根自身 SKILL.md 即单技能根，另扫一层子目录。系统提示尾部注
   入可用清单（`- 名: 描述 — 何时用 (file: 路径)`，描述展示截断 250
   字符；20KB 预算超限降级为仅名列——渐进披露）。`skill` 工具按名加
   载正文：去 frontmatter、100KB 截断并标注、附技能基目录（技能内相
   对文件的解析基），not-found 附可用名清单自纠；只读能力自动放行。
   坏技能（缺 name/description、名字含分隔符或空白、描述超限）记诊
   断跳过，不拖垮整层；读失败（非不存在）同样记诊断不静默。/skills
   终端命令列出技能与诊断。


46. webfetch 工具（ZCode WebFetch 的移植）：`{url, prompt?}`——抓取
   http(s) URL 并抽取可读文本。URL 归一：≤2000 字符、无凭据、http 升
   级 https、公网主机名（localhost/.local/裸单段主机拒绝）。egress
   守卫（SSRF）逐跳生效：字面量 IP 非公网段（RFC1918/环回/链路本地/
   CGNAT/组播/保留/文档段；IPv6 含 ULA/6to4；IPv4-mapped 与 NAT64
   64:ff9b::/96 先还原 IPv4 再判）一律拒绝；普通域名不做 DNS 预解析
   （preflight 在部分网络下让公网 URL 无谓失败——ZCode 同款取舍）。
   重定向手动逐跳：同主机（www 差异容忍、同端口）自动跟随（上限
   10 跳，超限报错），跨主机/协议/端口停在链上报告给模型（显式发
   起同样过门——透明而非强制）；跳转目标再过同一 egress 门（重定向
   是绕过首跳检查的经典通道）。响应 10MB 封顶；内容类型仅文本类
   （text/*、json/xml/js 及 +json/+xml 后缀；二进制报错）；HTML 剥
   script/style/注释、标题链接列表保形、实体解码；给模型 100K 字符
   封顶标注；HTTP ≥400 带状态与 4KB 错误页摘录回模型自纠。结果含
   URL/状态/类型/大小/耗时/跳转链头。短 TTL 缓存（15 分钟、50MB、
   最旧淘汰，按归一 URL 键控，进程级共享）。权限：预批准文档站清单
   （官方语言/框架文档，路径前缀站点防 %2f 编码绕过）只读自动放行
   （plan 模式可用）；其余主机中等风险询问（会话「总是允许」按 URL
   积累）。
   （规则 46 由 pending 的 webfetch PR 预留；44 的重号随其合并修正。）

47. read 工具读图片（多模态工具结果，ZCode read-image 的落地）：read
   对 png/jpeg/gif/webp 文件返回图片块——BlockImage 紧跟工具结果块进入
   同一工具结果消息（Content 仍是可读摘要：路径/类型/尺寸/字节数），
   随转录 JSONL 持久化。输入上限 20MB；超 2000px 维度或 3.75MB 二进制
   的 png/jpeg 缩放重编码（stdlib 双线性；gif 取首帧转 png；jpeg 质量
   85；收敛循环 0.7 步进），base64 ≤5MB 校验；webp 无 stdlib 解码器
   ——≤3.75MB 原样透传（尺寸未知不报维度），超限报错并提示先转 png。
   wire：Anthropic 把图片块与 tool_result 混排在 user 消息；Gemini 以
   inlineData part 同住 functionResponse 的 user content；OpenAI 的
   tool 消息只收文本——降级为文本说明（N 张图片未上送），模型至少看
   到摘要。SSH 工作区经网线读字节，同构。
1d468ec (feat: read tool returns images — multimodal tool results)

48. 工作流持久化与复用（ZCode saved-workflows / workflow-runs 的落
   地）：internal/workflow——定义存 dataDir/workflows/<名>.json（名限
   小写字母/数字/连字符，1..64；原子写；校验通过才保存），运行记录存
   dataDir/workflow-runs/<id>.json（开跑即落盘 running——崩溃后未完
   记录可续跑；终态回填每步结果/完成标记/错误；保留最近 50 条，开跑
   与收尾都裁）。workflow 工具三形态：{steps[, saveAs]} 临场执行可命
   名保存（保存不因运行失败而废——定义仍有效）；{run: 名} 从存储加
   载定义执行；{resume: 运行 ID} 续跑——已完成步骤（成功且定义未漂
   移）注入既往结果跳过执行，下游照常看到其文本（顺序模式按位、
   DAG 预置 done 状态；已完成运行拒绝续跑；命名运行的步骤表从定义
   取、临场运行从记录重建含 after）。zgo workflow list/show/runs 只读
   运维面。


49. SSH 连接保活与惰性重连（issue #220）：连接建立即起后台 keepalive
   循环（30s 量级周期发 keepalive@openssh.com，连续 3 次无回应判
   死）；Exec/ReadFile/ReadDir/Stat/Glob 等操作入口先确连接——死了
   用原 Config 透明重连（凭据在内存，in-flight 调用按现有错误路径
   干净失败，重试交模型自纠）；连续重连失败 5 次放弃（防风暴，切会
   话重建是唯一出路）。


50. 后台子代理（ZCode runtime-task registry / Agent 工具的落地）：
   `agent` 工具启动只读研究子代理并立即返回 ID——父会话不阻塞，继续回
   合；`agent_output {id}` 收割（running 报进度与耗时，done 返结论文
   本，failed/stopped 各有明确形态，未命中列既有 ID 自纠）；`agent_stop
   {id}` 停止（终态保持可读、结论为空）。注册表会话作用域（tools.
   States.Agents）：ctx 派生自注册表根而非工具调用——不随工具返回取
   消；DeleteSession/服务 Close 全停（不留僵尸循环）。嵌套循环的
   provider 与工具面与主循环同源：e2e 注入（UseProviderForTesting）
   经持锁快照进入子任务；ssh:// 会话的 task/dispatch/workflow/后台
   agent 一律经拨号后的 Remote 工作区（ReadOnlyW——此前字符串重建本
   地工具面，远程嵌套循环读的是本地磁盘）。

51. 自定义子代理定义（issue #225）：子代理 persona 用户可编程。定义
   层 internal/agents——两层平铺 markdown：工作区 `.zgo/agents/*.md` +
   用户家 `~/.zgo/agents/*.md`（同名工作区优先，与技能/命令同约定）；
   frontmatter：`name`（缺省取文件名主干）、`description`（路由依据，
   必填、≤1K 字符）、`tools`（逗号分隔白名单，取自子代理工具词表
   read/grep/glob/ls/edit/write/patch/todo；空即只读族，对齐现状
   SubtaskSystem）；正文即该子代理的系统提示（persona）。`task`/
   `dispatch` 增可选 `agent` 参数：命中则以定义 persona 替换
   SubtaskSystem、工具面按白名单从会话工具集选取（edit 族仍过主权限
   门——白名单只收窄不放宽，未列出的名字忽略）；未知名报错列出可用
   项自纠。主系统提示注入可用 agents 清单（name + description 一行
   式，对齐技能注入）。发现读上限 64KB、persona 载入上限 100KB（超限
   截断并标注），描述校验 1K——与注入家族同一套约束。

52. 子代理续话（ZCode SendMessage 的落地）：`agent_send {id, message}`
   对终态子代理追加用户消息、带全部既有历史（调查、工具结果、结论）
   后台重跑——同 ID 复用，收割面（agent_output）不变；运行中的任务拒
   绝续话（等收割或先停止，未命中列既有 ID 自纠）。运行器统一形态
   RunSubtaskSeeded（prior 为空即新任务、非空即续跑，历史拷贝隔离）；
   注册表在终态保存子代理转录，续跑翻转经持锁原子完成（并发续话只
   有一个生效）。

53. read 读 PDF（ZCode read-pdf 的落地）：.pdf 走 internal/pdftext 文
   本层抽取——词法级 stream 段收集 + FlateDecode 解压 + Tj/TJ/'/" 文
   本算子（Td/TD/T* 换行启发），字面串（转义/八进制/嵌套括号）与十六
   进制串（UTF-16BE BOM 检测）解码。有界：单流 16MB、产出 256KB rune
   截断标注。**可读率闸门**：产出可打印字符占比 <85% 即报错（CID 编
   码无 ToUnicode 的中文 PDF 常见）——宁报错并建议 bash 转换，不静默
   产乱码；无文本层（扫描件/图片型）同样报错。SSH 工作区经网线读字
   节同构。

54. 工作区信任门（pi project-trust / CC 信任提示的落地）：未授信的
   本地工作区不加载其 `.zgorules.json`（allow 规则即提权面——克隆来
   的恶意仓库不能靠自带规则文件给自己放权）、不加载其 `.zgo` 命令与
   技能层（提示注入面）；全局层不受影响。授信表 dataDir/trusted.json
   （路径键规范化：Clean + Windows 盘符路径整体小写；原子写；损坏即
   空表重起）。ssh:// 目标是用户显式拨号的远端——视为授信。授信途
   径：`/trust`（当前会话工作区，即时重载项目规则——在跑会话经
   ApplyProjectRules 生效）、`zgo trust <路径>`（print/CI 预授信）、
   `zgo trust --list / --revoke <路径>`（撤销即时清空在跑会话的项
   目规则）。未授信时终端三表面在会话建立时打 stderr 提示；桌面首开
   未授信工作区弹信任确认（路径 + 风险说明，「信任并加载」经服务授
   信落表、「暂不」仅关窗不授信）。

55. 场景评测（`zgo eval`，pi evals 的数据化形态）：用户可写 JSON 场
   景——种工作区文件（files）、脚本回合（tool+args 或 text，工具真实
   执行）、四类断言（transcript_contains 含工具结果块 / file_contains
   / no_tool_errors / event_count）。运行面与 e2e 同构：临时 dataDir +
   临时工作区（种文件即授信——场景是用户自己写的）+ 脚本 provider +
   自动放行 asker；关自动命名。目录默认 ./evals 不存在则 ~/.zgo/
   evals；--filter 按名/描述筛；任一失败退出码 1（可进 CI）。场景 Trusted 显式 false
   模拟未授信工作区（工作区层不加载）——**安全回归网**：trust-gate
   场景断言未授信的技能层不可读（信任门回归即红）、trust-granted 断
   言授信后可读（正向）。CI 以 `go run ./cmd/zgo eval evals` 为门禁
   步骤——任一场景失败即红。**第二批（规则 73/75/76 面）**：断言集
   增 transcript_not_contains（负向守卫——「必须不泄漏」类）；场景
   增 prompt 字段（用户消息可写——@引用 等用户侧展开据此驱动）；
   redaction-transcript（密钥打码：[REDACTED:…] 在、裸密钥不在）、
   custom-tool-trust-gate/granted（工具层信任门双向）、dir-mention
   （目录树展开）四场景常驻。

56. 模型兜底链（spec 规则 55 场景评测之后）：settings.fallbackModels
   （逗号列表，空即关闭）——rewire 的 Provider 把主模型与兜底序包成
   ai.NewFailover：流以**可重试**错误（429/过载/超时）收尾即换下一模
   型重发同一请求（模型是请求级字段，客户端复用）；非可重试错误（鉴
   权/参数）换模型无用，原样透传；全部耗尽透传末次错误并保持可重试
   （交上层整链再试）。e2e 注入不经兜底（脚本按模型无关回放）。配置
   面：zgo config get/set fallbackModels（写盘热生效；去空去重）。

57. 会话内搜索（/find，桌面搜索的终端对齐）：slash 表面钩子 Find——
   TUI 进入标记模式（大小写不敏感匹配转录行，命中行 ▶ 标记、当前命
   中 ▶；视口跳到最近命中；输入区为空时 n/N 下一处/上一处（环绕），
   有草稿不误触；孤立 Esc 先退搜索而非中止回合）；REPL 无视口——列
   命中清单（回合号 + 60 rune 片段，首 20 条 + 余量计数）。无钩子的
   表面明确报不支持。

58. MCP 配置命令（此前唯一途径是手编 settings.json，桌面亦无编辑面）：
   `/mcp add <名> -- <命令> [参数…]`（`--` 后按空白切分，无引号语义
   ——复杂命令进 settings.json；名非空无空白/斜杠，命令必填；上限
   32 个）、`/mcp remove <名>`（不存在报错）。服务面 SetMCPServer/
   RemoveMCPServer/MCPServers：写盘 + syncMCP 热重载（握手失败降级
   不阻断——诊断面可见）。

59. 后台代理可视化（spec 规则 50/51 的可见面）：`/agents` 列出本会话
   后台子代理（总数/运行数、状态标记 ▶运行带秒级耗时 ✓完成 ✕失败带
   错误 ■停止、任务首行 40 rune 截短、超 10 条折叠余量）；TUI 固定块
   （计划块同款，`代理 · N 运行/M` 头 + 4 条 + 折叠，空清单不占行）
   每次重画经 AgentJobStates 取快照——事件驱动的重画自然带最新状
   态。桌面面板为后续跟进（AgentJobStates 访问器已备）。

60. 问题报告（`zgo bugreport [路径]`，pi bug-report 的落地）：生成自包
   含 Markdown——版本/平台/数据目录、provider 配置（**脱敏**：key 只
   报已配置/未配置，值与 Hint 都不落盘）、模型与兜底链、每日预算、思
   考档位、授信计数、MCP 诊断（真探测）、会话概况（**计数 + 最近 5
   条元数据**——转录正文绝不进报告）、git/ssh 工具链探测。默认写工
   作区 `zgo-bugreport-<时间戳>.md`。

61. cron 任务级模型：`Job.Model`（cron add --model，此前只存不用——
   scheduleRunner 一直跑全局模型）。执行面 Manager.SetSessionModel：替
   换会话的模型解析闭包（s.model 是每次请求的实时读取点——后续回合
   与压缩即刻生效；空串恢复全局解析）；scheduleRunner 对 job.Model
   非空的任务建会话后覆盖。cron list 增模型列（空显示「（默认）」）。

62. 桌面后台代理状态条（spec 规则 50/51/58 的桌面可见面）：会话有后
   台子代理时聊天流上方显示状态条（`后台代理 · N 运行/M` + 每条
   ▶/✓/✕/■ + 耗时 + 任务摘要 28 rune 截短，超 4 条折叠；空不占位）。
   后台完成不触发重画——回合结束与会话打开拉一次快照并启动轮询
   （1s、单飞守卫、清空即退、1 小时上限防孤儿）；快照有变化才回主
   线程 update。

63. 子代理升级提问（ZCode escalate 的落地）：后台子代理的工具面加
   ask——遇到只有用户能答的问题时经会话提问通道升级，问题文本带
   `[后台代理 <id>]` 前缀（用户可对照 /agents 辨认来源）；答案作为
   工具结果回到代理转录，代理据此继续。AgentRunFunc 增 id 参数（升
   级前缀的来源）；ask 保持 SerialOnly 与只读风险。前台 task/
   dispatch/workflow 的研究步骤不带（短任务自纠，ZCode 同款取舍）。

64. cron 时间窗（pi off-peak 的 Zgo 形态）：`Job.Window`（"HH:MM-
   HH:MM" 本地时区，可跨午夜——22:00-06:00 取 [22:00,24:00)∪[0,
   06:00)；空即不限）——到期时刻在窗口外则**不 fire 也不动 LastRun**，
   任务保持到期，窗口一开的下一个 tick 补跑（语义是「推迟到窗口」而
   非「错过」）。cron add --window add 即校验；cron list 模型列附 ⏰
   窗口。

65. grep 远程并发（性能路线第一条）：ssh:// 工作区的 grep 逐文件拉取
   改 8 路并发（网络往返主导——RTT 并行化收益显著，16 文件 × 50ms
   RTT 顺序 800ms → 并发 ~200ms，实测并发峰值 ∈[2,8]）；结果按枚举
   序回填，归并上限语义与顺序版一致；候选上限 20000、单文件收集上
   限同总上限。**本地 grep 保持顺序 + 命中早停**——实测（Windows +
   Defender）本地文件打开被杀软串行化，并发零收益且稠密命中的早停
   是更大 win（读 67 个文件 vs 全量 2000）；逐文件 goroutine 的调度
   开销在小文件场景反超收益 20 倍（分块/分波两版实测后放弃）。

66. MCP 启动异步（性能路线第二条）：构造期 startMCP 在后台跑——
   mcp.Start 的握手最长 20s，同步曾让一台挂死的服务器把每个 zgo 命
   令的启动卡满超时；握手完成即入表，工具面随 mcpServers 快照自然出
   现。显式重载（设置变更/诊断）仍同步。syncMCP 串行化（并发为同
   名起两个进程）；**Close 守卫**：在途握手于服务关闭后才完成的直接
   关闭服务器不入表（异步启动的进程泄漏面）。

67. gopls 集成（LSP 路线 A v1，issue #222/#262）：internal/lsp——v1
   用 gopls CLI 子进程调用（definition/references/check 子命令，每次
   spawn 1-2s 起；daemon + JSON-RPC 是后续优化，工具面对模型同构）。
   三工具（纯只读、路径过 path jail）：`definition {file,line,column}`
   → 精确跳转点；`references {file,line,column}` → 被调点清单（符号语
   义——同名方法/注释/字符串不误报，grep 会）；`diagnostics {file?}`
   → 空参全工作区检查输出。前置约束：gopls 在 PATH 且工作区本地
   （ssh:// 不声明——文件不在本地盘；wasm 同）。缺 gopls 不声明工具
   组（模型可见面稳定；doctor 引导安装）。edit/write 成功回执尾部追
   加该文件即时诊断（gopls check，仅在有错时追加，10s 上限，缺 gopls
   静默跳过）。工具执行超时 30s、输出照家族截断。


68. Stop / Notification 命令钩子（issue #209）：CommandHooks 增
   `stop` 与 `notification` 两事件。**stop** 在回合自然收尾（无工具调
   用的 EndTurn，steering 排空之后）触发：payload {event, sessionId,
   workspace, lastText}；退出码语义对齐 Claude Code——exit 2 阻止收
   尾，stderr/输出作为原因注入续跑消息（用户消息形态「[stop 钩子] …」）
   进转录、模型继续工作；其他非零退出记日志不阻断；防死循环由既有
   MaxTurns 上限兜底。**notification** 在权限询问即将阻塞等待用户时触
   发（observer：payload {event, sessionId, workspace, tool, reason}，
   退出码不影响主流程）——headless/TUI 接系统通知的挂点。两钩子超时
   与 runHook 家族一致（10s，失败记日志放行）。


69. MCP 传输矩阵：stdio（既往）与 **Streamable HTTP**（2025-03 规范，
   issue #204）二选一——Config 增 `url` + `headers`（与 command 互斥，
   两者皆空皆有报错）。HTTP 传输：每条 JSON-RPC 消息 POST 到 url
   （Accept: application/json, text/event-stream），响应按 Content-Type
   分流——application/json 直读单响应；text/event-stream 解析 SSE 事件
   流取首个响应（通知/心跳忽略）。initialize 响应的 `Mcp-Session-Id`
   头记存，后续请求回传；响应 404 且已有会话时判定会话失效——清会话、
   重走 initialize 握手并重试原请求一次（断线重连不重启应用）。通知
   （notifications/initialized）POST 后接受 202 无响应。握手与
   tools/list、prompts、resources 流程与 stdio 完全共用（差异只在传
   输层）。鉴权 headers 全链路不落日志、不进会话 JSONL、不回显界面
   （对齐 API key 教义）；热重载签名覆盖 url+headers（哈希形态——值
   变更触发重载，但签名本身不可读出凭据）。

70. gopls 常驻 daemon（LSP 路线 A v2）：每个本地工作区根一个常驻
   gopls 进程走 LSP JSON-RPC（base protocol 分帧）——initialize 只付
   一次，definition/references 的冷启开销（每次 spawn 1-2s 起）摊平。
   生命周期：按需拉起；空闲 10 分钟回收；进程死亡/连接断同根透明重
   启（重试一次，再失败该次调用退回 v1 CLI 子进程路径——可用性优先
   于性能）；服务 Close 全量收割（不留孤儿进程）。位置编码：协商
   utf-8（与 CLI 字节列同口径）；老 gopls（v0.16.2 等未实现 3.17 协
   商）缺省 utf-16——查询与渲染两侧按行内容换算列单位（BMP 外字符
   按 2 unit），模型可见口径与 v1 完全一致。诊断：单文件 check 走
   拉取（textDocument/diagnostic）；编辑回灌先发 didChangeWatched
   Files 显式失效再拉（不依赖服务端文件监视器，磁盘改动即刻可见）；
   全工作区 check（无 LSP 等价）与未知子命令留 CLI 路径。服务端主
   动请求（workspace/configuration 等）必须回应——否则握手挂起。

71. 跨会话搜索索引（ZCode task-index 的 Zgo 形态）：internal/
   sessionidx——把每个会话 JSONL 的重放态有效文本抽成小写 haystack
   段（每消息：文本拼接段在前、工具调用参数段在后——与 matchSnippet
   的匹配优先序一致），持久化 dataDir/search-index.json（原子写、
   版本不符/损坏自愈为空重建）。查询走索引段（不读文件不解析
   JSON），未入索引的会话（单会话 corpus 超 1MB / 总量超 64MB / 文
   件损坏）退回 live 流式扫描——两路径消费同一抽取语义，命中与片
   段按构造等价（等价断言锚点）。保鲜：(size, mtime) 命中不重读；
   rewind/追加改 size 即触发重抽；会话删除剪条目。落盘异步（索引
   是加速器不是账本——写失败下次重建）。

72. 内置技能层（ZCode bundled-skills/skill-creator 的 Zgo 形态）：
   internal/skills/builtin——随二进制 go:embed 发行的高价值技能，首
   批三个：skill-creator（把可复用流程写成 .zgo/skills 的元技能——
   frontmatter 规范、description 即路由键、≤100 行）、go-testing（验
   证次序：窄跑→全量门→涉并发双跑；表驱动；进程类测试自管目录）、
   debug-methodically（先复现再读错误、一次一个假设、观察优先于改
   动、禁修症状）。层级与优先级：工作区 > 全局 ~/.zgo > 内置（根序
   先到先得，末位最低）——同名即覆盖，用户可改写内置行为而不动二
   进制。内置层不是注入面（内容随产品发行不读盘）：未授信工作区同
   样可用（信任门只挡工作区层）；发现/加载/系统提示注入走既有管道
   零特判（嵌入 FS 与本地/远端同一 Root 面）。

73. 工具输出密钥打码：红线「API key 不进会话 JSONL/不在界面回显」
   此前只在 bugreport 侧成立——模型跑 env/cat/read 时密钥会原样进入
   工具结果并落转录与导出。internal/redact 在 agent loop 的工具结果
   出咽喉点（execute 的 emit 前）统一打码：事件、wire、JSONL、导出、
   搜索索引全链路一处收敛；模型侧看到 [REDACTED:…] 不损失自纠。匹
   配两层：精确优先（运行时已知密钥——活动 key、全部剖面 key、
   OAuth 双 token，长者优先防前缀吞掉；短于 8 字符跳过——零误报）；
   模式族只收高置信命名前缀（OpenAI/Anthropic sk-、GitHub gh?_/
   github_pat_、AWS AKIA、Google AIza、JWT 三段），宁可漏不可错。
   开关 settings.redactSecrets（默认开；zgo config set——逃生口供
   生成测试夹具等需要原样输出的场景）；设置装载/rewire 时重喂（key
   轮换后旧值即失效）。用户手输消息不在打码面（用户的自觉行为，与
   工具侧机器泄漏是两个面）。**存量清洗**（收尾半边）：咽喉点只保
   护新写入，特性上线前的历史 JSONL 仍是泄漏面——`zgo sessions
   sanitize`（agent.SanitizeDir）逐行清洗：message 块（文本/工具参
   数）、compaction 摘要、meta 标题过打码器，其余行（usage/
   regenerate/未知类型/损坏行）原样字节保留；map 手术（RawMessage
   透传未触及字段——role/kept 等不丢）；默认 dry-run 只报数，
   --apply 原子重写（tmp + rename）；幂等（再跑零改写）；密钥面与
   打码器同源（当前设置的精确密钥 + 模式族）。

74. 会话记忆自动化（Claude Code auto-memory 的形态）：服务收尾
   （Close）时为有实质回合（首用户+助手问答都在）的会话跑一次小额
   补全（MaxTokens 192 / 15s 超时 / 同 provider），提炼「对未来会话
   有价值的持久事实」（用户偏好/项目约定/关键决定/重要路径），
   ≤3 行、每行 ≤120 rune、用户语言；「无持久事实」（纯问答/一次性
   任务）回复 none 即不写——不堆噪音。落盘走既有 workspace 记忆
   （memoryFor.Append 带时间戳条目；注入面 4KB 尾部窗口不变）。
   护栏：每会话每进程一次（内存 done 集）；工作区当日已有条目即跳
   过（重启重放最多一天一条）；触发点在 Close 且与自动命名解耦
   （两次后台补全并发会在脚本 provider 上交错不可测）——provider/
   MCP 拆卸前发、WaitGroup 落地后继续；失败记日志静默（记忆是增益
   不是账本）。开关 settings.autoMemory（默认开；zgo config set
   ——token 成本敏感用户的逃生口）。

75. 自定义工具（.zgo/tools）：commands/skills/agents 家族第四员
   ——把固定命令流程包装成模型可调用的工具。定义是 frontmatter
   （name/description 必填）+ 正文命令模板（$ARGUMENTS 占位；无占位
   符时参数追加在尾部——固定命令 + 可选参数两形态都成立）；发现两
   层平铺（工作区 `.zgo/tools/*.md` 覆盖全局 `~/.zgo/tools/*.md`，
   SSH 经网线）；描述 ≤1K、模板 ≤16KB。
   执行完全复用 bash 通道（超时/输出上限/权限门/path jail/远程同构/
   cwd 哨兵）——**权限按渲染后的最终命令判定**（只读探测看到的是
   真命令不是模板）；参数面单 string（args）。内置工具名优先：撞名
   的定义不声明（模型可见面不被覆盖）。**信任门**：未授信工作区不
   带其 .zgo/tools 层（工具模板是注入到执行的面——克隆来的恶意仓
   库不得自带可执行模板）；全局层不受影响。

76. @目录 引用展开（spec 规则 11 的目录形态）：@路径 命中目录时不
   再原样保留——展开为有界目录树：深度 ≤3、条目 ≤400、单树 ≤32KB，
   超限截断并标注；目录项尾部带 /、两层空格缩进；读不了的子目录静
   默跳过（坏链接不拖垮整棵树）；空目录/不可读目录不产出展开（引用
   保留原样）。预算与文本引用共享（mentionTotalCap/mentionMaxFiles
   ——目录树计入总量）；围栏同 FenceFor；本地与 SSH 远程同一 ListIn
   通道。

77. /tools 工具面清单：终端表面一命令列出本会话的模型可见工具
   ——与 loop 装配同一闭包（工具面装配存 buildTools 字段，Wiring
   与 /tools 同源），列出的就是模型看到的。来源分组标注：内置
   （无标记）/ 自定义（.zgo/tools，按工具实例类型判定——撞内置名
   的过滤在装配面，清单不会误标）/ MCP（mcp_ 前缀）；描述取首句
   ≤80 rune。自定义定义的非致命问题以 ⚠ 行附尾（parseDef 记诊断
   ——缺 name/description、模板为空），坏定义跳过不拖垮清单。

78. 调度并发硬化：定时任务 run 是完整 agent 会话——无上限并发既烧
   token 也抢 provider 配额。**全局并发上限**（maxConcurrentRuns=2）：
   满员的到期任务本轮不启动且 LastRun 不动（任务保持到期，下一
   tick 自然重试——**排队语义而非跳过**，不记 skipped 噪音）。
   **手动触发同源互斥**：RunNow（zgo cron run）不再绕过 single-
   flight——该任务已在跑（无论来自 tick 还是另一次手动）即拒绝并
   说明；空闲时照常执行并记录。计数与互斥共用 Scheduler 的锁域
   （running 集合 + active 计数）。

79. /context 上下文构成观测：「token 都花哪了」一命令可见——
   /context 列出下一请求的构成分段，与 loop 装配同源
   （SystemStableFS/SystemVolatile/memoryFor.Section/skills·agents
   Section/buildTools 的声明 JSON/Messages——测的即发的）：system·
   stable（缓存前缀）、system·git（易变段）、memory（4KB 尾窗注
   入）、skills、agents、tools（N 个工具声明）、transcript（N 条消
   息/M 用户回合）。字节口径（token 估算 ≈/4 只做参照）；合计行附
   /compact 提示。观测面导出 agent.Declarations（与 wire 请求同构）。

80. 跨会话搜索正则形态：SearchSessionsRegex（zgo sessions search
   --regex）——RE2（Go regexp 线性——无回溯炸弹）、**原大小写**
   （忽略大小写在模式里写 (?i)；子串形态保持既往小写语义）；编译失
   败错误透出自纠。命中器参数化（matcher：子串/正则同一段位窗逻辑
   ——片段口径两形态一致）；**正则不走索引**（索引面小写化——语义
   保真优先，全部 live 扫描；正则是低频路径，成本可接受）。子串形
   态与索引路径不变。

81. @文件 行范围语法：@path:lo-hi / @path:lo（单行）——大文件只
   带相关段。1 起行窗：lo<1 clamp 1、hi 超文件尾 clamp（尾随换行产
   的空段不算一行）；lo>hi 交换；围栏头注明实际范围（@ten.txt:8-10）；
   窗口内容照过 60KB rune 截断与总量预算（#120 护栏自动覆盖）；同文
   件不同窗各自展开（seen 键含窗）；无范围形态不变。数字形态由正则
   限定，病态大数按无范围处理 Picture-free（>1<<30 拒绝）。

82. 会话归档（ZCode tasks-index 组织态的 Zgo 形态）：组织态与内容
   正交——归档不删内容（转录/搜索/导出面不变），只影响「活跃面」。
   meta 行增 archived 标志（SessionInfo.Archived；meta 首行重写持
   久——与 Rename 同路径）；`zgo sessions archive|unarchive <id|前
   缀>`；list 默认隐藏归档（--archived 只看归档、--all 全量）；
   `-c/--continue` 恢复跳过归档会话（同工作区取最近未归档）。跨会
   话搜索照常搜归档内容（内容可查、组织态只管列表）。

83. 文件快照存储（非 git 工作区的检查点降级路径）：git 临时索引方
   案对非仓库返回 ErrNotARepo 后此前只剩「仅回退转录」——这里补另
   一半：工作区整树按相对路径复制进 dataDir 快照库（snapshots/
   <session>/<seq>/）；恢复是镜像回写（快照内文件覆写、快照后新增
   文件删除——对齐 git 路径 read-tree -u --reset + clean -fd 语义）。
   上限：文件 5000 / 总量 100MB——超限 ErrTooLarge 降级回既往行为
   （大目录维持仅回退转录，不因快照失败阻塞回合）；符号链接与系统
   噪音（.DS_Store/Thumbs.db）跳过；.git 防御性不入库。错误分层对
   齐 git 路径：旧会话无快照（ErrSnapshotMissing）→ 降级；有快照
   但被修剪（ErrSnapshotPruned）→ 中止回退（issue #152 语义）。
   半截快照不留（失败即清会话根——空根会让 Restore 误判）。会话
   删除清快照库（SnapshotRemoveAll，与 checkpoint refs 同步）。

84. 远程工作区快照（spec 规则 83 的远程半边）：ssh:// 会话此前完全
   降级（连「仅回退转录」的文件半边都没有）——捕获经拨号后的
   RemoteFS 整树拉进本地快照库（ReadFile/ReadDir 递归；单文件读失
   败跳过不拖垮整树）；恢复是镜像推回——快照内 WriteFile 覆写、
   快照后新增经 Exec rm -f 删除（路径单引号包裹——注入面封闭）；
   上限/保留窗口/错误分层（Missing 降级/Pruned 中止）与本地快照
   同源。远程路径根相对斜杠形态（RemoteFS 约定）。

85. 硬预算熔断：budgetDailyTokens 此前只是软预算（80%/100% 各发一
   次 EvBudgetNotice 不阻断）——settings.hardBudgetStop（默认关；
   zgo config set）开启后，今日已计价 token **达预算即拒新回合**：
   ChatMedia 入口熔断（明确报错、不进转录——止损闸在回合开始前；
   错误文案带解除命令）。cron 调度同闸（scheduleRunner 走 Chat 入
   口——定时任务也烧预算）。跨天由当日记账的重放机制自然解开（新
   一天新文件）。开关与预算均落盘（重启后闸仍在）。

86. 会话置顶（组织态的另一半，与规则 82 归档成对）：meta 行增
   pinned 标志（SessionInfo.Pinned；首行重写持久——与 Archive 同路
   径）；`zgo sessions pin|unpin <id|前缀>`；**置顶组在列表最前**（组
   内与普通组内均按更新时间新前——Manager.List 排序键升级为
   pinned 优先 + 时间次之）；list 表格 📌 前缀标记。置顶只影响列表
   次序——内容/搜索/恢复/导出面不变（组织态与内容正交）。归档与置
   顶可并存（归档过滤在先——--archived 视图内置顶同样排前）。

| 状态          | 所有者                        | 说明                          |
| ------------- | ----------------------------- | ----------------------------- |
| 消息历史      | `agent.Session`               | 唯一写入路径是 loop 的 append |
| 运行中标志    | `agent.Session`（RunState）   | idle / running / aborted      |
| provider 配置 | `ai.Resolver`（env > 设置文件）| 解析一次，会话期间不变        |
| workspace     | `tools.Workspace`             | 每个 session 一个 root        |

## 接口

```go
// internal/ai
type Provider interface {
    Stream(ctx context.Context, req Request) <-chan StreamEvent
}

// internal/agent
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args json.RawMessage) ToolResult
}
// 可选：声明权限姿态（只读自动放行；alwaysAsk 每次确认）
type Capabler interface{ Capability() permission.Capability }

// 权限（ZCode permission/ 翻译）：allow/ask/deny 三态，
// 会话级与项目级规则 + 工具能力声明；ask 经阻塞回调问用户。
Permissions *permission.Service
Ask func(AskRequest) AskResponse

// 压缩（ZCode compact/ 翻译）：window−reserve−buffer 阈值触发，
// 摘要 + 尾部保留替换历史；连续失败 3 次熔断。
Session.Compact(ctx) / autoCompact at Chat entry

// internal/service（UI 唯一入口）
Chat(sessionID, text string, emit func(agent.Event)) error
Abort(sessionID string)
CompactSession(id string) error
```

## 事件序列（验收基线）

```
agent_start
  turn_start
    message_start(assistant)
      text_delta* | tool_call_delta*
    message_end(stop_reason, usage)
    [tool_execution_start, tool_execution_end]*   # 每个 toolCall 一对
  turn_end
agent_end(stop_reason)
```

- error / aborted 的 message_end 后不再有 tool_execution_*，直接 turn_end → agent_end。
- 事件即 UI 协议：新增字段必须向后兼容（新增可选，不重命名不删除）。

## 验收场景

1. 纯文本问答：一轮、无工具调用、事件序列如上。
2. 工具调用：模型请求 read → 工具结果入历史 → 模型基于结果作答。
3. 截断保护：伪造 stopReason=length 的含工具调用回复 → 工具不执行，
   历史中出现 isError 的 toolResult。
4. 中止：Chat 进行中调用 Abort → 事件流以 aborted 结束，Session 可再次 Chat。
5. Path jail：read("../outside.txt")、read(绝对路径越界) → isError=true。
6. 持久化：Chat 后进程重启，LoadSession 恢复完整历史。
9. 附件：composer 添加的图片随消息发送 —— provider 请求中该用户消息含
   image 块（文本在前），转录持久化后重读仍在。
7. DAG 工作流：三步 A、B（无依赖）、C after A,B —— A、B 并行启动，C 等两者
   完成后启动且其提示携带 A、B 的结果；A 失败时 C 不启动，报告 A 失败与 C 跳过。
8. 检查点回滚：git 工作区中一回合改了文件后「回滚最近一回合」，转录截断到
   该回合前且工作区文件（含该回合新建/删除的）回到回合前状态；分支与提交
   历史不变；ignored 文件保留。
9. 工具 diff：edit/write 的工具块事件携带统一 diff —— 新建文件全为 + 行，
   修改为 -旧/+新 配上下文行；desktop 工具块与终端 TUI 彩色渲染。
11. @ 引用：发送含 "@文件" 的消息后，provider 请求的该用户消息带围栏
    文件内容（token 保留）；不存在的引用原样；桌面输入 @ 前缀浮出提示
    并可点选补全。
12. 审批预览：write 的权限询问携带 +内容 预览；edit 携带 -旧/+新；
    询问未回答前文件不落盘。
13. patch：多段补丁的审批预览按文件分节展示全部改动；拒绝后零落盘。
15. 惰性装载：重启后列表即时可用；Messages 首次调用载入完整转录且幂等；
    惰性往返后继续追加并再次重启，消息数守恒；启动耗时可测地低于全量
    解析（同机对照）。
14. 终端附件：-i 与 /attach 路径经同一 loadImageBlocks 校验入块；
    下一条消息携带图片块；坏路径可读报错且不入队。
16. 前缀缓存：三处稳定前缀各带一个 ephemeral 断点且总数 ≤4；最后一条
    （正在变动的）消息不带断点；空前缀零断点不崩。
17. E2E 硬化：并行批次（被权限询问阻塞的 write 不拖累自动放行的
    read）、流中途中止（text_delta 后挂住，Abort 后以 aborted 收尾且会话
    可继续）、瞬态重试（首个尝试 429 重试，EvTurnRetry 可见，重放同一
    提示后成功）。harness 的 Turn 增加 ErrorOnce 与 BlockAfterText 两个
    脚本原语。
18. 任意回合回滚：两轮会话回滚到第 2 轮前撤掉第 2 轮文件与转录、保留第
    1 轮；再回滚到第 1 轮前全部撤空只剩底座；运行中被拒；越界拒绝。
