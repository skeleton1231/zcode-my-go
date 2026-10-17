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
| chatState       | uiapp 中事件流折叠出的视图状态（派生数据，不落库）                |
| chatItem        | chatState 的一行：用户消息 / assistant 消息 / 工具块              |
| Settings        | 用户配置：baseURL / apiKey / model；env 优先于文件                |
| View            | native UI 的视图函数：每帧由状态构建（package ui）                |
| Frame           | 一次视图构建；`ui.Tester.Frame()` 在测试中驱动                    |
| win.Update      | 跨 goroutine 把状态变更排回主线程重绘的机制                       |
