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

每个功能一次提交与一次 PR（不在 main 上直接开发）：

```bash
git checkout -b feat/<功能名>        # 或 fix/ chore/ docs/
# …实现 + 测试…
go build ./... && go vet ./... && go test ./...
git add -A && git commit              # 约定式消息：<type>: <描述>
git push -u origin HEAD
gh pr create --fill-first             # PR 描述含变更摘要与测试计划
```

- commit 消息与 PR 描述如实写明验证结果；未跑过的检查不写成通过。
- PR 粒度 = 一个功能；循环（/loop）产出的每轮迭代就是一个 PR。

### 并行会话用 worktree 隔离

多会话/agent 并行时（如 /loop 验证 PR 与人工开发同时进行），主目录只保留
main，验证与开发一律进独立 worktree，避免 stash/checkout 他人的未提交改动：

```bash
git worktree add ../zcode-wt/<名称> -b <type>/<名称> main
cd ../zcode-wt/<名称>               # 此目录内 build/vet/test/commit
git worktree remove ../zcode-wt/<名称>  # 用完清理；先确认无 go 进程占用文件
```

- 验证已有 PR 分支：`git worktree add ../zcode-wt/pr-N <PR分支名>`。
- 主目录存在其他会话的未提交 WIP 时，绝不 `git stash`、`git checkout --` 等
  改写其工作区的操作。

## 命令与仓库结构

纯 Go 工程，无 Node/Bun 依赖：

| 用途              | 命令                        |
| ----------------- | --------------------------- |
| 构建              | `go build ./...`            |
| Vet               | `go vet ./...`              |
| 测试（含视图测试）| `go test ./...`             |
| 桌面开发（热重载）| `go tool mygo dev`          |
| 打包安装包        | `go tool mygo build`        |
| 浏览器目标（wasm）| `go tool mygo build -platform web && go run ./cmd/webfs` |

```
main.go               桌面壳入口：窗口、菜单、ui.View
main_wasm.go          浏览器目标：用户数据目录指向 wasm 内存 FS
cmd/zgo               终端三形态：print(-p)、REPL、全屏 TUI(-t)
internal/ai           LLM provider 抽象（Anthropic 兼容流式）
internal/agent        agent loop、事件、会话与 JSONL 持久化、子代理、工作流
internal/agents       自定义子代理定义（.zgo/agents/*.md 发现与 persona）
internal/agent/permission  权限系统（ZCode permission/ 翻译）
internal/agent/compact    上下文压缩（ZCode compact/ 翻译）
internal/tools        内置工具：read/write/edit/bash/grep/glob/ls/apply_patch/webfetch/
                      agent 三件套（后台子代理 spawn/output/stop）

                      agent 四件套（后台子代理 spawn/output/stop/send 续话）
                      todo/ask/memory/task/dispatch/workflow
internal/prompt       系统提示（环境、git、项目上下文文件）
internal/service      AgentService：UI 唯一入口（设置、会话、Chat 回调、
                      权限/提问通道、压缩入口）
internal/uiapp        native UI：chatState 折叠 + App 视图 + 弹窗
specs/                行为规格（先于代码）
internal/checkpoint  工作区检查点与回滚（git 临时索引快照 + read-tree 恢复）
internal/schedule    定时自动化（cron 解析/任务存储/分钟调度器，zgo cron）
internal/skills      技能发现与加载（SKILL.md frontmatter、两层根、按名加载）
internal/clipimg    剪贴板位图读取（PNG 注册格式优先，DIB→PNG；贴图入口）
internal/workflow    工作流定义与运行记录持久化（saveAs/run/resume）
internal/mcp          MCP 客户端（stdio JSON-RPC）
internal/webfetch    webfetch 工具（URL 归一、SSRF egress 守卫、重定向策略、文本抽取、缓存）
internal/pdftext     PDF 文本层抽取（read 读 PDF 的内核，可读率闸门）
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
