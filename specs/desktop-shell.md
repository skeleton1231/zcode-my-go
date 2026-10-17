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
8. 附件（多模态输入）：📎 打开图片选择（png/jpg/gif/webp，多选），以
   chips 展示、发送前可移除；随下一条消息发送（文本必填）。单张上限
   5MB，超限或类型不符以 notice 提示。终端形态不支持附件。
7. 浏览器目标（wasm）：`go tool mygo build -platform web` 产出静态站点，
   `go run ./cmd/webfs` 注入内存文件系统垫片（wasm 的 syscall 层需要 Node
   形态的 fs 对象，wasm_exec.js 的回退只有 stdout）。浏览器上的语义：
   用户数据目录与工作区都在内存 FS（`main_wasm.go` 的 SetPath 与
   PickWorkspace 的 GOOS=js 分支），垫片把每个文件的改动（去抖合并）逐条
   写入本源 IndexedDB，并在 wasm 实例化前恢复——刷新不清空设置、会话与
   工作区文件（API key 随设置文件存于本源 IndexedDB，仅本地）；
   bash 工具明确报错拒绝（无进程模型）；权限确认弹窗照常阻塞运行；
   provider 经浏览器的 fetch 流式调用。

10. 终端面板（菜单「终端」/CmdOrCtrl+J）：聊天与输入区之间的底部抽屉，
   在当前会话的本地工作区跑用户 shell（mygo terminal 插件）。终端与工作
   区绑定，切换会话即重建；SSH 远程工作区经 remote.Shell 打开交互远端 shell
   （PTY + WindowChange 尺寸传播；拨号失败退化为提示）；库缺失或创建失败降级为错误提示，不崩；关闭面板与应用退出
   都终止 PTY 子进程。
9. 文件树面板（菜单「文件树」/侧栏「文件」/CmdOrCtrl+B）：右列展示当前
   会话工作区的目录树（目录优先排序，懒展开，经服务过 path jail——本地与
   SSH 远程同源）；点文件打开只读预览弹窗（60KB 截断、二进制拒绝）；
   刷新清空缓存；切换会话清空树缓存。
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
6. 浏览器目标：静态站点加载后窗口渲染；新建会话落在内存工作区；设置
   弹窗保存的配置经真实 provider 流式对话；写工具触发权限弹窗，未点击
   时运行阻塞；bash 工具返回明确的不可用错误。
7. 浏览器持久化：写文件 + 保存设置后刷新页面，会话列表与完整历史
   （含工具块）、设置（模型与 key）、工作区文件全部恢复；恢复后的会话
   可继续运行（读回文件并作答）。
8. 终端：打开面板即在当前工作区获得交互 shell；工厂失败显示「终端不可
   用」提示且重开重试；切会话换目录重建；关闭面板释放子进程。
7. 文件树：打开面板列出工作区（目录在前）；展开目录见子项；点文件出
   预览内容；切会话后树为新工作区的。
9. 终端（远程）：ssh 会话打开面板得到远端交互 shell；键入可达、输出回流、
   关闭即断。
