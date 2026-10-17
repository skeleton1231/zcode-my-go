# AGENTS.md

Zgo 是开源桌面 coding agent：mygo 桌面壳 + Go 实现的 agent 核心（provider 抽象、
agent loop、工具）+ React 前端。对标 pi 的架构分层与 ZCode 的工程规范。

## 核心原则

- 新增或修改行为前，先更新对应 spec（`specs/`），再实现代码；spec 是验收基线。
- 分层不可跨越：前端 → `internal/service` → `internal/agent` → `internal/ai` /
  `internal/tools`。禁止 UI 直接 import agent 核心，禁止 service 绕过 agent 直调
  provider。
- 每层职责单一：`ai` 只管协议与流式，`agent` 只管循环与历史，`tools` 只管文件与
  进程，`service` 只管绑定与生命周期。
- 避免重复状态与多条写入路径：消息历史的唯一写入者是 agent loop；UI 是派生视图。
- 定位问题时先调查原因再改代码；修复 bug 时用中文注释说明原因和依据。
- 有行为改动时先补测试（`go test ./...` 必须过），报告真实结果。

## 命令与仓库结构

| 用途              | 命令                          |
| ----------------- | ----------------------------- |
| 构建（全平台检查）| `go build ./...`              |
| Vet               | `go vet ./...`                |
| 测试              | `go test ./...`               |
| 桌面开发          | `bun run dev`（仓库根）       |
| 前端类型检查      | `bun run --cwd frontend typecheck` |
| 重新生成 TS 客户端| `bun run --cwd frontend generate` |
| 打包              | `bun run build`               |

```
main.go               桌面壳入口：窗口、菜单、Bind
internal/ai           LLM provider 抽象（Anthropic 兼容流式）
internal/agent        agent loop、事件、会话与 JSONL 持久化
internal/tools        内置工具：read/write/edit/bash/grep/glob/ls
internal/service      AgentService：前端唯一入口（mygo.Bind）
internal/prompt       system prompt 构造
frontend/             Vite + React 前端（生成客户端在 frontend/src/mygo.ts）
specs/                行为规格（先于代码）
docs/                 架构文档
```

## 实现与验证

- Go 1.27+（`go.mod`）。Windows 为一等平台：路径处理一律 `filepath`，shell 工具
  在 Windows 走 PowerShell。
- 工具的文件访问必须过 path jail（`tools.Workspace`）；越界即错误。
- 工具输出必须截断；新增工具要定义输出上限。
- 错误显式处理：不吞错、不用超时掩盖同步问题。
- API key 只经设置文件与内存，不进日志、不进 git、不进会话 JSONL。
- 事件协议（`agent.Event`）只加不改：新增字段可选，不重命名不删除。

## 安全边界

- bash 工具有执行超时（默认 120s）与输出上限；不做命令黑名单（模型可见结果自纠），
  但必须保留 abort 传播。
- 不在日志、示例或提交中写入凭据、真实用户数据。
