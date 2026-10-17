# Zgo 架构

一个二进制的桌面 coding agent：mygo 桌面壳（系统 WebView，无 cgo）包着 Go
agent 核心，前端是 Vite + React 静态产物。

```
┌─ frontend (React) ────────────────────────────────────────────┐
│  App → agent.ts reducer（事件流 → 视图模型）                    │
│        ↕ mygo typed IPC（生成客户端 frontend/src/mygo.ts）       │
├─ internal/service AgentService（mygo.Bind；前端唯一入口）─────────┤
│        ↓ Chat(sessionID, text, Channel[Event])                 │
├─ internal/agent ──────────────────────────────────────────────┤
│  Session（JSONL 持久化、abort）→ Loop（回合循环、事件流）        │
│        ↓ Provider 接口               ↓ Tool 接口               │
├─ internal/ai ────────────────┬─ internal/tools ────────────────┤
│  Anthropic 兼容 SSE 客户端    │  read/write/edit/bash/grep/…    │
└──────────────────────────────┴─────────────────────────────────┘
```

## 分层与依赖

依赖方向受 `architecture-policy.yaml` 治理，单向无环：

- `ai`：LLM 协议边界。`Provider.Stream` 消息进出、工具声明、SSE 解析。
  契约（学 pi 的 StreamFn）：任何失败都不 panic，一律以
  `EvMessageEnd{StopError|StopAborted}` 收尾，channel 恰好关闭一次。
- `agent`：回合循环（`loop.go`）。要点：
  - `stopReason=length` 时本轮工具调用一律不执行，改写为错误结果
    （截断的参数可能不完整）；
  - 事件流即 UI 协议（`events.go`），新增字段可选、不重命名不删除；
  - 转数上限 `DefaultMaxTurns` 防死循环；
  - 历史唯一写入路径是 `Session.Append`（loop 经由它追加）。
- `tools`：实现 `agent.Tool` 接口（接口定义在消费方）。所有文件访问过
  `Workspace.Resolve` 的 path jail；所有输出有上限
  （`tools.go` 的常量表）；bash 在 Windows 走 PowerShell，带超时。
- `service`：设置解析（env > 设置文件 > 默认值，key 不出 Go 侧）、
  会话生命周期、`mygo.Channel` 事件转发；页面断开时中止运行。
- `frontend`：只 import 生成客户端；`agent.ts` 是纯函数 reducer，
  把事件流折叠成视图模型（`agent.test.ts` 覆盖）。

## 持久化

- 会话：`<userData>/sessions/<id>.jsonl`，首行 meta、后续每消息一行，
  追加写不改写；损坏行跳过不致命。
- 设置：`<UserData>/settings.json`（0600），API key 仅存于此与内存。

## 线程模型

mygo 的绑定方法各跑在独立 goroutine；`Session` 以 mutex 保护
历史与运行标志（锁序 Manager → Session，无环）。abort 通过
`context.CancelFunc` 贯穿 provider 流与工具执行。

## 已知边界（v0.1）

- 工具按序执行（sequential）；并行执行与 steering 队列是接口预留。
- 上下文无 compaction：超长会话需新建会话。
- GLM 兼容端点流式响应不报 input_tokens（message_delta 兼容补报已加）。
