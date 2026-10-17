# Spec: desktop-shell

Status: implemented (v0.2, native UI)

## 范围

mygo 桌面壳：窗口生命周期、菜单、native UI（package `ui`，纯 Go 自绘，无
webview、无 HTML/JS、无前端构建）与 `internal/service` 应用服务层。
壳不含任何 agent 逻辑。

## 产品规则

1. 界面用 mygo 的 package `ui` 写成：状态结构体 + `view(c *ui.Context)`
   方法，每帧由状态构建；无前端工具链（运行时与开发时都不需要 Bun/Node）。
2. UI 只通过 `internal/service` 的公开方法消费 agent 核心（见
   specs/agent-core.md 的事件序列）；事件在 `win.Update` 内应用到视图状态，
   agent 回合跑在独立 goroutine 上。
3. 设置（baseURL / apiKey / model）经设置弹窗（`ui.Modal`）读写，落盘到
   用户数据目录；API key 只存本地文件，不进日志、不进会话持久化、不在
   界面回显。
4. 会话侧栏支持：新建（系统目录选择对话框）、列出（含标题与运行状态）、
   切换、删除（菜单触发）。
5. 消息区：流式 assistant 文本用 `markdown.Stream`，完成后 `markdown.View`；
   列表跟随末尾（FollowEnd）；工具调用渲染为可折叠块（名称 · 参数摘要 ·
   状态点，展开见完整输出）。
6. 输入区：多行自动增高（`TextArea.Lines(1,8)`），Enter 发送 /
   Shift+Enter 换行；运行时按钮变为停止（触发 Abort）。

## 状态所有者

| 状态     | 所有者           | 说明                              |
| -------- | ---------------- | --------------------------------- |
| 会话列表 | `service`        | UI 调 `ListSessions` 拉取镜像     |
| 视图状态 | `uiapp.App`      | 只在 view（主线程）与 win.Update 内改 |
| 流式文本 | Go 端事件源      | 视图状态是派生，不落库            |

## 验收场景

1. 冷启动：无会话时展示引导文案；无设置时提示先配置。
2. 流式渲染：text_delta 经 win.Update 上屏，列表吸底；工具块可折叠展开。
3. 中止：停止按钮触发 Abort，界面回到可输入状态。
4. 会话切换：历史经 `Messages` 恢复（含工具结果）。
5. 视图测试：`ui.NewTester` 无窗口运行 view，输入并提交后用户消息与
   agent_end 事件出现在视图状态中。
