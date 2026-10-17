# Changelog

约定式提交驱动的里程碑摘要；每一行都对应一个经完整验证与 PR 评审的变更。

## v0.4.0 — workbench 与自动化

- **工作区检查点与文件回滚**：git 临时索引快照，回滚最近一回合/编辑重发/
  重试同时恢复转录与文件（分支与历史不动，字节保真）。
- **diff 全链路**：edit/write/patch 工具块彩色统一 diff；权限确认**批准前
  预览**（试算不落盘）；patch 多文件按段聚合。
- **多模态输入**：图片附件（三协议）+ `@路径` 引用展开（三端同享）。
- **定时自动化**：`internal/schedule` + `zgo cron` + 桌面任务面板；桌面应用
  随启动开调度器。
- **终端面板与文件树**：底部抽屉工作区 shell（Ghostty vt 内核）；右列文件
  树 + 只读预览（本地与 SSH 同源）。
- **审批与并发加固**：权限询问单槽串行化；并行批次同路径读改写串行化；
  remote Exec 并发排空与输出上限；附件消息预算兜底。
- **E2E 测试体系**：`internal/e2e` 脚本化 provider 驱动全栈（工具往返、权限、
  插话、压缩、检查点、附件、@ 引用、定时运行、diff）。
- **浏览器目标**：`mygo build -platform web` + 内存 FS 垫片 + IndexedDB
  持久化——完整 agent 跑进浏览器，刷新不清空。

## v0.3 — ZCode 核心翻译（三轮收官）

- 权限系统（三态/规则/能力声明/plan-mode）、上下文压缩（阈值/熔断/
  microcompact）、todo/ask、bash 命令级只读判定。
- apply_patch 多文件补丁、分层记忆（memory 工具 + 提示注入）、多步工作流
  （权限门继承）、task 子代理、MCP 客户端（stdio）。
- 三协议 provider：Anthropic 兼容（thinking 预算）/ OpenAI / Gemini；
  终端三形态：print / REPL / 全屏 TUI。

## v0.2 — native UI 定型

- React 前端 → mygo 纯 Go 自绘（零 webview、零前端工具链）。
- 会话/聊天/设置/权限弹窗骨架；JSONL 持久化；agent loop 事件协议。

## v0.1 — 初始骨架

- provider 抽象 + Anthropic 兼容流式；read/write/edit/bash 四工具与 path
  jail；桌面壳起步。
