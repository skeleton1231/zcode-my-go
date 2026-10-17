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
    Stream(ctx context.Context, req Request) StreamResult // 流式事件通道
}

// internal/agent
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args json.RawMessage) ToolResult
}

// internal/service（前端唯一入口）
Chat(sessionID, text string, out *mygo.Channel[agent.Event]) error
Abort(sessionID string)
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
