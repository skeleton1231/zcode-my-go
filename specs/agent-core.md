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
   则）。

37. zgo config CLI（终端的设置面，与桌面设置面板同源）：
   `config list` 全量（key 永不回显，Hint 形态）；`config get <键>` 单值；
   `config set <键> <值>`——model/api/baseUrl/thinking（同 SaveSettings
   校验：非法 api/thinking 拒绝）、apiKey（写后同样只回 Hint）、
   budgetDailyTokens（≥0 整数，0=不限）。写盘即热生效（rewire/syncMCP
   同既有路径）；未知键/缺值可读报错。

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
