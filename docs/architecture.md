# Zgo 架构

一个纯 Go 二进制的桌面 coding agent：mygo native UI（package `ui`，无
webview、无前端构建）包着 Go agent 核心。

```
┌─ internal/uiapp ── native UI（package ui）─────────────────────┐
│  App（视图状态）· chatState（事件流折叠）· View 每帧构建        │
│        ↓ 直接方法调用（App.update → win.Update 回主线程）        │
├─ internal/service AgentService（UI 唯一入口）───────────────────┤
│        ↓ Chat(sessionID, text, emit func(Event))               │
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
  会话生命周期、`Chat` 以回调转发事件；`PickWorkspace` 用系统对话框。
- `uiapp`：native UI 层。`chatState` 是事件流的纯函数折叠（视图状态，
  不落库）；`App.View` 每帧由状态构建。线程模型：视图与状态修改都在主
  线程，agent 回合 / 对话框 / 设置保存在独立 goroutine，经 `App.update`
  （有窗口时 `win.Update`，测试时直接执行）回到主线程。

## 持久化

- 会话：`<userData>/sessions/<id>.jsonl`，首行 meta、后续每消息一行，
  追加写不改写；损坏行跳过不致命。
- 设置：`<UserData>/settings.json`（0600），API key 仅存于此与内存。

## 线程模型

视图函数跑在主线程，每帧重建；跨 goroutine 的状态变更必须走
`win.Update`。`Session` 以 mutex 保护历史与运行标志（锁序
Manager → Session，无环）。abort 通过 `context.CancelFunc` 贯穿
provider 流与工具执行。

## 测试

- `internal/agent`、`internal/ai`、`internal/tools`：单元测试
  （fake provider、httptest SSE、临时目录）。
- `internal/uiapp`：`chatState` 折叠的单元测试 + `ui.NewTester`
  无窗口视图测试（打开会话→发送→终止、设置弹窗保存）。
- 真实端点冒烟：`ZGO_SMOKE_BASE_URL/KEY/MODEL` 驱动
  `TestSmokeRealProvider`（CI 默认跳过）。

## 已知边界（v0.2）

- 工具按序执行（sequential）；并行执行与 steering 队列是接口预留。
- 上下文无 compaction：超长会话需新建会话。
- GLM 兼容端点流式响应不报 input_tokens（message_delta 兼容补报已加）。
- 工具块删除会话无确认对话框（菜单触发，后续可加 `ui.Modal` 确认）。
