# CONTEXT.md — 领域词汇

修改相关代码 / UI 前先统一术语，命名以本表为准。

| 术语            | 含义                                                              |
| --------------- | ----------------------------------------------------------------- |
| Session         | 一次持续对话：消息历史 + workspace + 元数据；持久化为 JSONL        |
| Turn            | 一轮 LLM 调用：assistant 流式回复（可能含工具调用）→ 工具结果批次 |
| Message         | 历史中的一条消息：user / assistant / toolResult                   |
| Block           | 消息内的内容块：text / toolCall / toolResult                      |
| Event           | agent loop 对外的流式事件（UI 协议），见 specs/agent-core.md      |
| Tool            | 模型可调用的能力，JSON Schema 参数 + Execute                       |
| Provider        | LLM API 适配器（Anthropic 兼容协议：Anthropic / GLM / Kimi…）     |
| Workspace       | 工具的文件访问根目录（path jail 边界）                            |
| StopReason      | 一轮结束原因：end_turn / tool_use / length / error / aborted      |
| Steering        | agent 运行中插入的用户消息（排队，下一 drain 点注入）             |
| Settings        | 用户配置：baseURL / apiKey / model；env 优先于文件                |
| Channel         | mygo 的 Go→页面流式通道（`mygo.Channel[T]`）                       |
