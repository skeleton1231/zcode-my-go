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
   （~/.zgo/ZGO.md）+ 自工作区向上到 git 顶（含）每层第一个命中的上下
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
34. diff 持久化与导出：tool-result 块携带 Diff（edit/write/patch
   等改动类工具的统一 diff，随转录 JSONL 持久化——此前只在事件流上
   存在，重载/导出即丢）；HTML 导出以彩色行渲染（+绿/-红）、
   Markdown 以 diff 围栏（```diff）呈现于工具输出之后；HTML 导出同
   时补齐 thinking 块（<details> 折叠，与 Markdown 对称）。
30. TUI 输入历史（CC 的 ↑/↓ 召回）：每条已提交的用户消息入历史
   （连续重复折叠）；输入行上 ↑ 向旧召回、↓ 向新，回到最新一行恢复
   召回前的草稿；编辑召回文本后回车即发送（不改动历史本体）；边界不
   动。会话切换（/resume//new）不清历史——召回跨会话可用。
31. MCP 资源（MCP 三件套收口）：握手并行探测 resources/list（能力
   可选，不支持的服务器静默无资源）；/mcp 列表同时列出资源（server:
   uri 形态，标注描述与媒体类型）；`/mcp @server:uri` 经 resources/read
   读取文本内容并以「资源 <uri>：」围栏并入消息直接发送（同 stdin 管
   道形态——动态围栏按内容最长反引号串 +1 加长，issue #178：Markdown
   资源含行首裸 ``` 不得破栏）；非文本资源（无 text 块）给可读报错；
   未知服务器/资源报错并列出可用项。
## 状态所有者

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
