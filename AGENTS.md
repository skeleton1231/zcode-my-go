# AGENTS.md

Zgo 是开源桌面 coding agent：mygo **native UI**（package `ui`，纯 Go 自绘，
无 webview、无前端构建）+ Go 实现的 agent 核心（provider 抽象、agent loop、
工具）。对标 pi 的架构分层与 ZCode 的工程规范。

## 核心原则

- 新增或修改行为前，先更新对应 spec（`specs/`），再实现代码；spec 是验收基线。
- 分层不可跨越：uiapp → service → agent → ai / tools / prompt。禁止 UI 直接
  import agent 核心，禁止 service 绕过 agent 直调 provider。
- 每层职责单一：`ai` 只管协议与流式，`agent` 只管循环与历史，`tools` 只管文件与
  进程，`service` 只管设置与会话生命周期，`uiapp` 只管视图状态与渲染。
- 视图是状态的纯函数：状态只在 view（主线程）与 `win.Update` 内修改；agent
  回合与对话框跑在独立 goroutine，经 `App.update` 回到主线程。
- 避免重复状态与多条写入路径：消息历史的唯一写入者是 agent loop；视图状态是
  事件流的折叠（`chatState.apply`）。
- 定位问题时先调查原因再改代码；修复 bug 时用中文注释说明原因和依据。
- 有行为改动时先补测试（`go test ./...` 必须过），报告真实结果。

## Git 工作流

每个功能一次提交与一次 PR，且**在 worktree 里开发**（主检出目录留给
人工操作与浏览，循环/多会话互不踩踏）：

```bash
# 1. 建隔离工作树（基于目标基线分支）
git fetch origin
git worktree add .claude/worktrees/<名称> -b feat/<功能名> origin/main
cd .claude/worktrees/<名称>

# 2. 实现 + 测试 + 全量验证
go build ./... && go vet ./... && go test ./...

# 3. 提交并开 PR（约定式消息：<type>: <描述>）
git add -A && git commit
git push -u origin HEAD
gh pr create --fill-first             # PR 描述含变更摘要与测试计划

# 4. PR 开出后清理本工作树（分支已推远端，PR 不受影响）
cd ../.. && git worktree remove .claude/worktrees/<名称>
git worktree prune
```

- commit 消息与 PR 描述如实写明验证结果；未跑过的检查不写成通过。
- PR 粒度 = 一个功能；循环（/loop）产出的每轮迭代就是一个 PR。
- 并发会话各自用独立 worktree；主目录只读浏览，不并行 checkout。
- Go 模块缓存全局共享，worktree 无需重新拉依赖；`build/` 产物各树独立。

## 命令与仓库结构

纯 Go 工程，无 Node/Bun 依赖：

| 用途              | 命令                        |
| ----------------- | --------------------------- |
| 构建              | `go build ./...`            |
| Vet               | `go vet ./...`              |
| 测试（含视图测试）| `go test ./...`             |
| 桌面开发（热重载）| `go tool mygo dev`          |
| 打包安装包        | `go tool mygo build`        |

```
main.go               桌面壳入口：窗口、菜单、ui.View
cmd/zgo               终端三形态：print(-p)、REPL、全屏 TUI(-t)
internal/ai           LLM provider 抽象（Anthropic 兼容流式）
internal/agent        agent loop、事件、会话与 JSONL 持久化、子代理、工作流
internal/agent/permission  权限系统（ZCode permission/ 翻译）
internal/agent/compact    上下文压缩（ZCode compact/ 翻译）
internal/tools        内置工具：read/write/edit/bash/grep/glob/ls/apply_patch/
                      todo/ask/memory/task/workflow
internal/prompt       系统提示（环境、git、项目上下文文件）
internal/service      AgentService：UI 唯一入口（设置、会话、Chat 回调、
                      权限/提问通道、压缩入口）
internal/uiapp        native UI：chatState 折叠 + App 视图 + 弹窗
specs/                行为规格（先于代码）
internal/mcp          MCP 客户端（stdio JSON-RPC）
docs/                 架构文档 + ZCode 移植地图（zcode-port-map.md）
mygo.json             打包配置（name/identifier/version/out）
```

## 实现与验证

- Go 1.27+（`go.mod`，CLI 以 tool 指令引入）。Windows 为一等平台：路径处理
  一律 `filepath`，shell 工具在 Windows 走 PowerShell。
- 工具的文件访问必须过 path jail（`tools.Workspace`）；越界即错误。
- 工具输出必须截断；新增工具要定义输出上限。
- 错误显式处理：不吞错、不用超时掩盖同步问题。
- API key 只经设置文件与内存，不进日志、不进 git、不进会话 JSONL、不在界面回显。
- 事件协议（`agent.Event`）只加不改：新增字段可选，不重命名不删除。
- 视图测试用 `ui.NewTester`（无窗口）；改动 `uiapp` 视图后跑
  `go test ./internal/uiapp/`。

## 安全边界

- bash 工具有执行超时（默认 120s）与输出上限；不做命令黑名单（模型可见结果
  自纠），但必须保留 abort 传播。
- 不在日志、示例或提交中写入凭据、真实用户数据。
