# Spec: desktop-shell

Status: implemented (v0.1)

## 范围

mygo 桌面壳：窗口生命周期、菜单、`AgentService`（mygo.Bind 的服务层）、
Vite + React 前端。壳不含任何 agent 逻辑。

## 产品规则

1. UI 永远不直接 import agent/ai/tools 包；只通过 `AgentService` 的导出方法与
   `mygo.Channel` 事件流交互（见 specs/agent-core.md 的事件序列）。
2. Chat 调用通过 `Channel[agent.Event]` 流式接收事件；UI 按 sessionID 路由，
   与调用者窗口解耦。
3. 设置（baseURL / apiKey / model）经 `GetSettings`/`SaveSettings` 读写，
   落盘到用户数据目录；API key 只存本地文件，不进日志、不进会话持久化。
4. 前端是纯静态产物（`frontend/dist`），无运行时 Node/Bun 依赖。
5. 会话侧栏支持：新建、列出（含标题与更新时间）、切换、删除。

## 状态所有者

| 状态     | 所有者          | UI 侧               |
| -------- | --------------- | ------------------- |
| 会话列表 | `AgentService`  | 只读镜像 + 事件刷新 |
| 流式消息 | Go 端事件源     | 派生渲染，不落库    |

## 验收场景

1. 冷启动：无设置时展示配置入口；配置后可发起对话。
2. 流式渲染：text_delta 增量上屏不闪烁；工具块可折叠展开。
3. 中止：Stop 按钮触发 Abort，UI 立即回到可输入状态。
4. 多窗口：mygo 事件与会话状态一致。
