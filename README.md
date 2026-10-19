# Zgo

<div align="center"><strong>Zgo — 属于你的开源桌面 Coding Agent</strong></div>
<div align="center">纯 Go · mygo native UI · 零 Webview · 零前端工具链</div>

<p align="center">简体中文 | <a href="README.en.md">English</a></p>

---

Zgo 是一个开源的桌面 coding agent，**纯 Go 单二进制**：

- 基于 [mygo](https://github.com/egoist/mygo) 的 **native UI**（package `ui`，
  Metal / Direct3D 11 / OpenGL 上由 Go 自绘）：无 HTML、无 JavaScript、无
  webview 进程、无 Bun/Node —— 开发与运行都只要 Go。
- **agent 架构源自 pi**（[earendil-works/pi](https://github.com/earendil-works/pi)）：
  provider 抽象、工具调用循环、类型化事件流、截断调用保护。
- **工程规范源自 ZCode**：spec 先行（`specs/`）、架构治理
  （`architecture-policy.yaml`）、领域词汇（`CONTEXT.md`）、UI 设计规范
  （`DESIGN.md`）。

```
native UI (package ui)  ──▶  AgentService  ──▶  agent loop
   纯 Go 自绘视图              设置 · 会话         ├─▶ ai（Anthropic 兼容 SSE）
   win.Update 跨线程           生命周期           └─▶ tools（read/write/edit/bash/grep/glob/ls）
```

## 特性

- **流式对话**：SSE 流式回复，text_delta / 工具调用 / usage 事件经
  `win.Update` 直达自绘界面（Markdown 流式渲染）。
- **工具执行**：读 / 写 / 精确编辑 / shell（Windows 走 PowerShell）/ grep /
  glob / ls，path jail 限制在 workspace 内，输出自动截断。
- **会话持久化**：JSONL 追加式历史，重启恢复。
- **多 Provider**：任何 Anthropic 兼容端点 —— Anthropic 官方、智谱 GLM
  （`https://open.bigmodel.cn/api/anthropic`）、Kimi 等。
- **中止安全**：随时停止，LLM 流与运行中工具一并取消，会话可继续。
- **即时启动**：无 webview 进程，窗口秒开、内存占用小。
- **权限系统**（译自 ZCode permission/）：allow/ask/deny 三态、会话级
  「总是允许」规则、只读工具自动放行；桌面弹窗或终端 stdin 确认。
- **上下文压缩**（译自 ZCode compact/）：token 预算阈值自动摘要 + 尾部
  保留，连续失败熔断，也可 `Ctrl+K` 手动压缩。
- **任务清单与提问**：todo 工具跟踪计划（会话级状态，桌面任务面板与
  TUI 顶部计划块同步刷新）；ask 工具把只有用户能答的问题送达界面。
- **终端三形态**：`zgo -p "任务"` print 模式、行式 REPL（/compact /plan
  /new /rewind /retry /resume /export /attach /usage /perms /mcp）、`zgo -t
  全屏 TUI（ANSI 备用屏 + 流式刷新 + 任务面板 + Ctrl+↑/↓ 回合导航 + Esc
  中止）；`-c/--continue`、`-r/--resume <前缀>` 跨进程恢复会话，运行中可
  插话（下一回合注入）；REPL/TUI 行编辑与 ↑/↓ 历史、括号粘贴（多行不
  逐行提交）。
- **多文件补丁**：apply_patch 工具（Add/Update/Delete，上下文唯一匹配）。
- **长期记忆**：workspace 级记忆跨会话存活，自动注入系统提示。
- **子代理与工作流**：task 工具派发只读研究；workflow 工具编排多步计划，
  edit 步骤继承权限门与会话规则。
- **MCP**：settings 配置 stdio MCP 服务器，其工具以 mcp_ 前缀接入。
- **三协议**：Anthropic 兼容 / OpenAI / Gemini，设置里一键切换。
- **订阅登录**：`/login`（Claude Pro/Max OAuth，PKCE + 本地回调 + 自动刷新），
  三端可用；无 API Key 时以订阅身份调用。
- **插话与并行**：运行中插话（steering）注入下一回合；单批工具调用并行执行
  （同路径读改写自动串行化），子代理与 DAG 工作流并行扇出。
- **Hook**：PreToolUse/PostToolUse 内部钩子 + 设置文件里的外部命令钩子。
- **SSH 远程工作区**：会话目标 `ssh://user@host:port/path`，read/write/edit/
  bash/grep/glob/ls/patch 全量经 SSH 客户端在远端执行，同样的 path jail。
- **工作区检查点与回滚**：git 工作区每回合前快照（临时索引，不触碰真实
  索引），「回滚最近一回合」/任意回合回滚（桌面 ⟲ 回滚到此、终端
  `/rewind [n]`，撤掉第 n..末轮的文件与转录）/编辑重发/重试同时恢复
  转录与文件（分支与历史不动）。
- **diff 全链路**：edit/write/patch 的工具块彩色展示统一 diff；权限确认弹窗
  **批准前预览**待执行改动（试算不落盘）；patch 多文件按段聚合。
- **多模态输入**：composer 📎 添加图片（三协议 base64/dataURL/inlineData）；
  消息内 `@路径` 引用自动展开文件内容（三端同享，path jail）。
- **定时自动化**：`zgo cron` 管理定时任务（五字段 cron，jobs.json 热重载，
  分钟调度器，防重叠），桌面「定时任务」面板可视管理；桌面应用随启动开。任务可指定
  覆盖模型与时间窗（HH:MM-HH:MM 可跨午夜，窗口外推迟不漏跑）。
- **终端与文件树**：桌面底部抽屉终端（工作区 shell，Ghostty vt 内核）；
  右列文件树 + 只读预览（本地与 SSH 远程同源）。
- **浏览器目标**：`go tool mygo build -platform web && go run ./cmd/webfs`——
  完整 agent 跑进浏览器（内存 FS 垫片 + IndexedDB 持久化，刷新不清空）。
- **跨会话搜索**：`CmdOrCtrl+Shift+F` 一框检索全部会话（文本与工具参数，
  点命中直达）。
- **性能**：启动惰性装载会话（只读 meta 行）；Anthropic 前缀缓存三断点，
  命中按缓存计价。
- **CLI 运维家族**：`zgo cron`（定时任务）、`zgo doctor`（环境诊断）、
  `zgo sessions`（列表/导出/删除/搜索）、`zgo memory`（工作区记忆）、
  `zgo models`（模型目录与校验）、`zgo usage`（会话 token 用量与缓存
  命中率）。
- **脚本化输出**：`--output-format json` 逐事件 JSONL（稳定字段名，args
  原始内嵌）；stdin 管道并入提示词——`git diff | zgo -p "review"
  `--output-format json` 全链自动化；`zgo config` 终端设置面（写盘热
  生效）。
- **每日 token 预算护栏**：当日记账跨会话共享（缓存读 0.1 计价、午夜翻
  转），80%/100% 阈值通知四表面渲染——软预算不阻断；`/perms` 查看本会
  话的总是允许规则。
- **MCP 三件套**：工具 + 提示词（`/mcp server.prompt` 展开直发）+ 资源
  （`/mcp @server:uri` 读取直发）。
- **CI**：GitHub Actions 双平台（windows/ubuntu）build/vet/test + wasm
  交叉编译与 Linux -race。
- **会话生命周期钩子**：settings 配 sessionStart/sessionEnd（观察者）
  与 preCompact（非零退出阻止压缩），外部命令同机制。
- **层级项目上下文**：~/.zgo/ZGO.md 用户全局 + 自工作区向上到 git 顶
  逐层择一（ZGO/AGENTS/CLAUDE.md，浅→深、8KB 封顶）；远程工作区同享。
- **后台子代理**：agent 工具启动后台研究代理（启动即返 ID、父会话继续
  工作），agent_output 收割、agent_stop 停止、agent_send 带全部历史续
  跑；/agents 与 TUI/桌面状态条实时可见。遇到只有用户能答的问题时经会话
  提问通道升级（带代理 ID 前缀），答案回转录继续。
- **技能（skills）**：SKILL.md + frontmatter 的可复用指令包（工作区
  .zgo/skills 覆盖全局 ~/.zgo/skills，SSH 同构）；系统提示注入可用清
  单（超预算降级仅名列），skill 工具按名加载正文。
- **webfetch**：抓取 URL 抽取可读文本（HTML→文本）——逐跳 SSRF 守卫
  （私网/环回全拒）、同主机重定向自动跟随（跨主机停链报告）、10MB/
  100K 双封顶、15min 缓存；官方文档站只读放行，其余主机询问。
- **工作区信任门**：未授信的本地工作区不加载其 .zgorules.json（allow
  规则即提权面）与 .zgo 命令/技能层——克隆来的恶意仓库不能给自己放
  权；/trust 授信（即时重载）、`zgo trust` CLI、桌面首开确认对话框。
- **工作流持久化**：saveAs 命名保存、{run} 按名执行、{resume} 续跑
  （已完成步骤注入既往结果跳过，定义漂移防护）；`zgo workflow` 查看。
- **模型兜底链**：settings.fallbackModels——主模型 429/过载时按序降级
  重发（致命错误透传不兜底）；cron 任务可指定自己的模型。
- **read 读 PDF 与图片**：PDF 文本层抽取（可读率闸门，乱码宁报错）；
  工作区图片以图片块进模型上下文（三协议适配）。
- **场景评测 + 安全回归网**：`zgo eval` 用 JSON 场景钉回归（种文件/
  脚本回合/断言，失败退出码 1）——CI 门禁步骤常驻运行，信任门有
  双向场景守卫（门回归即红）；/find 会话内搜索（TUI 标记跳转）。
- **MCP 配置命令**：`/mcp add <名> -- <命令> [参数…]` / `/mcp remove`
  ——免手编 settings.json，写盘热重载。
- **问题报告**：`zgo bugreport` 生成脱敏自包含报告（可直接附 issue）。
- **会话导出即分享**：HTML / Markdown 自包含导出（无上传服务——个人 agent
- **gopls 语义工具**：definition / references / diagnostics（符号语义，
  同名文本不误报）；编辑回执即时诊断回灌；常驻 daemon（LSP JSON-RPC，
  冷启摊平，失败退 CLI 子进程）。
- **跨会话搜索索引**：重放态文本段持久化 + (size, mtime) 增量保鲜——查询
  不再逐行解析全部 JSONL；超限/损坏退 live 扫描（结果等价）。
- **密钥打码闭环**：工具输出过打码器（事件/wire/JSONL/导出一处收敛）——精确
  密钥 + 高置信模式族双面；`zgo sessions sanitize` 清洗历史转录（dry-run
  预览，--apply 原子重写）。
- **自定义子代理**：`.zgo/agents/*.md` 定义 persona；task/dispatch 的 agent
  参数命中即走（白名单收窄工具面）。
- **内置技能层**：skill-creator / go-testing / debug-methodically 随二进制发行；
  工作区 > 全局 > 内置三层，同名即覆盖。
- **MCP 双传输**：stdio 与 Streamable HTTP（2025-03 规范）——远端服务器免本地
  子进程；启动异步（不再等握手）。
- **provider 剖面**：`/provider` 一键切换多 API 账号/端点（命名剖面，key 同一条
  脱敏边界）。
- **剪贴板贴图**：composer Ctrl+V 直接贴截图（DIB→PNG）。
- **Stop/Notification 钩子**：回合收尾钩子（exit 2 阻止收尾续跑）与权限询问
- **会话记忆自动化**：收尾提炼持久事实进工作区记忆（≤3 行，当日去重，
  可关）。
- **自定义工具**：`.zgo/tools/*.md` 命令模板包装成模型可调用工具（权限按渲染后
  命令判定）；/tools 一命令查看工具面。
- **@目录 / @行范围**：@目录 展开有界目录树；@file.go:120-180 只带相关行段。
- **/context 上下文构成**：下一请求的构成分段一命令可见（测的即发的）。
- **搜索正则**：`zgo sessions search --regex`（RE2、原大小写）。
- **会话归档**：archive/unarchive 组织态（list 默认隐藏、-c 跳过——内容不删）。
  通知挂点。
  的分享物就是导出文件本身）。

- **快照全覆盖**：非 git 工作区与 ssh:// 远程会话的检查点补全——整树
  快照进数据目录（超限降级），回滚/编辑重发/重试三入口与 git 路径同语义。
- **预算经济学**：硬预算熔断（达预算拒新回合，默认关）；子代理与工作流
  的嵌套循环用量全部入账；`zgo usage` 按会话/来源归因 + `--days=N` 近
  N 日趋势条形；📌 会话置顶。
- **遥测**：默认关的 span 追踪（turn/model/tool/compact）——file 按日
  JSONL 或 OTLP 导出；属性与错误打码，导出失败绝不阻塞 agent。
- **MCP 服务端**：`zgo mcp serve` 把工具面暴露成 stdio JSON-RPC 服务端
  （默认只读四件，--all 开全量）——ZCode/Claude Code/Cursor 可直接调用
  zgo 的 path-jail 工具。

## 快速开始

只依赖 Go 1.27+：

```bash
go tool mygo dev      # 开发模式（热重载）
go test ./...         # 全部测试（含无窗口视图测试）
go tool mygo build    # 打包（.exe 安装器 / .dmg / .deb）
go run ./cmd/zgo -p "解释这个仓库"   # 终端模式
```

更多形态：

```bash
zgo cron add --schedule '*/10 * * * *' --prompt '看看构建状态' --workspace .   # 定时任务
zgo cron list && zgo cron run <id>                                             # 查看 / 立即执行
go tool mygo build -platform web && go run ./cmd/webfs   # 浏览器版（wasm 静态站点）
```

首次启动后在设置弹窗填入：

| 配置     | 示例                              | 说明                     |
| -------- | --------------------------------- | ------------------------ |
| Base URL | `https://api.anthropic.com`       | 或 GLM / Kimi 兼容端点   |
| API Key  | `sk-...`                          | 只存本地，不回显         |
| Model    | `claude-sonnet-4-5` / `glm-5.2` …| 端点支持的任意模型 ID    |

也可用环境变量：`ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`、`ZGO_MODEL`、
`ZGO_THINKING`（env 优先于设置文件）。

### 项目权限规则

在工作区根放 `.zgorules.json`，权限规则随仓库走（团队成员共享）：

```json
{
  "version": 1,
  "rules": [
    {"tool": "bash", "args": "git *", "behavior": "allow"},
    {"tool": "write", "behavior": "ask"},
    {"tool": "bash", "args": "rm -rf *", "behavior": "deny"}
  ]
}
```

规则按序匹配；`args` 是通配模式（`*` 任意），可写 `"command: git *"`
的规范形态，或 `"git *"` 直接匹配命令本体。解析失败降级为空规则并记
日志，绝不阻塞会话。

## 工程文档

- [`AGENTS.md`](AGENTS.md) — 给 coding agent 的工程规范（分层、验证命令）
- [`specs/`](specs/) — 行为规格，先于代码
- [`architecture-policy.yaml`](architecture-policy.yaml) — 模块依赖治理
- [`CONTEXT.md`](CONTEXT.md) — 领域词汇表
- [`DESIGN.md`](DESIGN.md) — UI 设计规范
- [`docs/architecture.md`](docs/architecture.md) — 架构说明
- [`docs/zcode-port-map.md`](docs/zcode-port-map.md) — ZCode 全模块移植地图

## License

MIT © 2026 skeleton1231
