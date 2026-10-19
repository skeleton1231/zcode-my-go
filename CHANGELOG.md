# Changelog

约定式提交驱动的里程碑摘要；每一行都对应一个经完整验证与 PR 评审的变更。

## v0.11.0 — 快照全覆盖与用量经济学

- **文件快照存储**（#283）：非 git 工作区检查点补全——整树快照进
  数据目录（上限 5000 文件/100MB 超限降级），恢复镜像回写；错误分
  层与 git 路径同源（Missing 降级/Pruned 中止）。
- **远程工作区快照**（#284）：ssh:// 会话检查点补全——RemoteFS 整
  树拉本地快照库、恢复推回（覆写 + 删多余，路径单引号封闭注入面）。
- **硬预算熔断**（#285）：达预算拒新回合的止损闸（默认关）——明确
  报错不进转录、cron 调度同闸、落盘重启仍在、跨天重放自然解开。
- **会话置顶**（#286）：组织态另一半——📌 置顶优先于时间序（组内
  稳定），与归档正交；CLI pin/unpin。
- **子代理用量记账**（#287）：task/dispatch/后台代理的嵌套循环用
  量入当日记账（此前对预算完全不可见）；熔断后子代理启动被拒。
- **工作流步骤用量**（#288）：workflow 步骤用量透传入账——嵌套循
  环三入口的用量面闭环。
- **用量归因**（#289）：当日按会话与来源（主循环/子代理/工作流）
  归因——`zgo usage` 一命令可见「哪个会话烧了预算」。
- **用量历史趋势**（#290）：`zgo usage --days=N` 近 N 日每日总量 +
  迷你条形（缺日照列 0 保趋势连续）。
- **遥测子系统**（#292，spec 规则 91）：默认关的 span 追踪——turn/
  model/tool/compact 四类 span；file（按日 JSONL）或 OTLP 导出；
  属性与错误打码、导出失败只计数绝不阻塞 agent。
- **MCP 服务端模式**（#294，spec 规则 92）：`zgo mcp serve` 把工具
  面暴露成 stdio JSON-RPC 服务端——默认只读四件（无确认通道的安
  全默认），--all 显式开全量；结果打码；path jail 照常。

（合并序：#292 → #294 → 本发布 PR。）

## v0.10.0 — 记忆、自定义工具与可观测面

- **会话记忆自动化**（#272）：服务收尾为有实质回合的会话提炼一次
  持久事实（用户偏好/项目约定/关键决定，≤3 行）进工作区记忆——下
  次会话经既有 4KB 注入面生效；与自动命名解耦、当日去重、失败静
  默；settings.autoMemory 默认开可关。
- **自定义工具**（#273）：`.zgo/tools/*.md` 命令模板（$ARGUMENTS
  占位）包装成模型可调用工具——执行完全复用 bash 通道（权限按渲
  染后命令判定）；两层平铺同名覆盖；未授信工作区不带其工具层。
- **@目录 / @行范围**（#274/#280）：@目录 展开有界目录树（深度
  3/条目 400/单树 32KB）；@path:lo-hi 只带相关行段（clamp/交换、
  围栏头标注真实行号）——大文件引用省上下文两件套。
- **/tools 工具面清单**（#275）：模型可见工具一命令列出（与 loop
  装配同源）；来源分组（内置/自定义/MCP）；坏定义 ⚠ 行。
- **eval 安全回归网第二批**（#276）：transcript_not_contains 负向
  守卫 + prompt 场景字段；打码/自定义工具信任门/@目录四场景常驻
  CI。
- **调度并发硬化**（#277）：全局并发上限（同时 2 个 run——满员到
  期任务排队不跳过）；RunNow 手动触发与 tick 同源互斥。
- **/context 上下文构成观测**（#278）：下一请求的构成分段一命令
  可见（系统提示/记忆/技能/工具声明/转录——测的即发的）。
- **跨会话搜索正则**（#279）：`zgo sessions search --regex`——RE2
  线性、原大小写（(?i) 忽略）、坏模式报错自纠。
- **会话归档**（#281）：组织态与内容正交——archive/unarchive 只影
  响活跃面（list 默认隐藏/--archived/--all、-c 跳过），转录/搜索/
  导出不变；meta 行持久。

## v0.9.0 — 语义工具、索引加速与密钥闭环

- **gopls 语义工具**（#263/#265）：definition/references/diagnostics
  三工具（符号语义——同名方法/注释/字符串不误报，grep 给不出的精
  度）；edit/write 成功回执回灌该文件即时诊断（错误直接带回模型自
  纠）。常驻 daemon 走 LSP JSON-RPC（initialize 只付一次，冷启摊
  平）；位置编码协商 utf-8、老 gopls 缺省 utf-16 按行内容双向换算；
  崩溃透明重启、失败退 CLI 子进程。
- **跨会话搜索索引**（#267，性能路线）：重放态文本段持久化
  search-index.json，(size, mtime) 增量保鲜（未变会话零文件读）；
  查询不再逐行解析全部 JSONL；超限/损坏会话退 live 扫描——两路径
  同一抽取语义，命中与片段按构造等价。
- **密钥打码闭环**（#269/#270）：红线「API key 不进会话 JSONL/不在
  界面回显」全链路落地——agent loop 工具结果咽喉点统一打码（事件/
  wire/JSONL/导出/搜索索引一处收敛）；精确密钥（活动+剖面+OAuth，
  长者优先）+ 高置信模式族（sk-ant/sk-/gh?_/AKIA/AIza/JWT）双面；
  `zgo sessions sanitize` 清洗历史转录（默认 dry-run，--apply 原子
  重写，未触及行字节不动）。
- **自定义子代理**（#259）：`.zgo/agents/*.md` 定义 persona（两层
  平铺，同名工作区优先）；task/dispatch 增 agent 参数（命中走
  persona、白名单收窄工具面）；可用清单注入主系统提示。
- **内置技能层**（#268）：bundled skills 随二进制 go:embed 发行——
  skill-creator / go-testing / debug-methodically 三个起步；三层根
  （工作区 > 全局 > 内置，同名即覆盖）；未授信工作区同样可用。
- **MCP Streamable HTTP**（#266）：传输矩阵补齐 2025-03 规范——
  远端服务器不再依赖本地子进程；与 stdio 同一 mcp_ 前缀接入。
- **MCP 启动异步**（#258，性能路线）：New 不再等握手（20s 超时曾把
  每个 zgo 命令卡满）；显式重载仍同步；顺带修异步启动暴露的
  mcpServers 裸读竞态（CI -race 逮到）。
- **provider profiles**（#261）：`/provider` 切换命名 API 剖面（多
  账号/多端点一键换），key 与主设置同一条脱敏边界。
- **剪贴板贴图**（#260）：composer Ctrl+V 直接贴截图（DIB→PNG，
  降级走 📎 选择器）——截图进上下文零摩擦。
- **Stop/Notification 命令钩子**（#264）：stop 在回合自然收尾触发
  （exit 2 阻止收尾、输出作为原因注入续跑）；notification 在权限
  询问即将阻塞时触发（headless/TUI 接系统通知的挂点）。

## v0.8.0 — 能力广度与安全边界

- **后台子代理四件套**（#237/#239/#249/#252）：agent（启动即返 ID、父
  会话不阻塞）、agent_output（收割：运行报进度、完成返结论）、
  agent_stop、agent_send（终态代理带全部历史续跑，同 ID 复用）；
  /agents 与 TUI 固定块、桌面状态条三面实时可见（1s 轮询、变化才刷）。
  顺带修复：嵌套循环 provider 绕过 e2e 注入、ssh:// 会话嵌套工具面读
  本地磁盘两个真实缺陷。
- **工作区信任门**（#243/#244）：未授信的本地工作区不加载其
  .zgorules.json（allow 规则即提权面——克隆来的恶意仓库不能给自己放
  权）与 .zgo 命令/技能层；ssh:// 显式拨号视为授信；/trust 即时重载、
  zgo trust CLI（grant/list/revoke，撤销即时清空）；桌面首开信任确认
  对话框。
- **技能子系统**（#231）：SKILL.md + frontmatter 两层根（工作区覆盖
  全局，SSH 同构）；系统提示渐进披露注入（20KB 预算降级仅名列）；
  skill 工具按名加载（not-found 附可用清单自纠）。
- **webfetch 工具**（#232）：URL 归一 + 逐跳 SSRF egress 守卫（私网/
  环回/CGNAT/NAT64 全拒）+ 同主机重定向自动跟随（跨主机停链报告）+
  HTML→文本抽取（10MB/100K 双封顶）+ 15min 缓存；预批准文档站只读放
  行、其余主机询问。
- **工作流持久化**（#236）：定义与运行记录落盘（开跑即记、终态回填、
  保留 50）；workflow 工具 {steps[,saveAs]}/{run}/{resume}——续跑注
  入既往结果跳过已完成步骤（定义漂移防护）；zgo workflow CLI。
- **模型兜底链**（#246）：fallbackModels——主模型可重试失败（429/过
  载）按序降级重发；致命错误透传不兜底；config get/set。
- **read 读 PDF/图片**（#234/#242）：PDF 文本层抽取（可读率闸门——CID
  乱码宁报错不产垃圾）；图片以 BlockImage 随工具结果进模型上下文
  （三协议 wire 各自适配/降级）。
- **场景评测**（#245）：zgo eval——JSON 场景（种文件/脚本回合/四类断
  言）驱动真实回放，失败退出码 1 可进 CI；/find 会话内搜索（TUI 标记
  跳转 + REPL 清单，spec 规则 57）。
- **MCP/运维面**：/mcp add/remove（免手编 settings.json，热重载）、
  cron 任务级模型（Job.Model 补执行面 + 会话模型覆盖）、zgo bugreport
  （脱敏自包含问题报告）。
- **子代理升级提问**（#254）：后台研究代理遇到只有用户能答的问题时经
  会话提问通道升级（[后台代理 <id>] 前缀），答案回转录继续跑——ZCode
  escalate 的落地。
- **cron 时间窗**（#255）：Job.Window（HH:MM-HH:MM 可跨午夜）——窗口
  外到期不 fire 不动 LastRun、窗口一开补跑（推迟而非错过）；与任务级
  模型构成自动化经济学（便宜模型 + 闲时窗口）。
- **eval 安全回归网 + CI 门禁**（#256）：场景 Trusted:false 模拟未授
  信工作区——trust-gate/trust-granted 双向断言信任门（门回归即 CI
  红）；CI 增 zgo eval 门禁步骤。
- **grep 远程并发**（#257，性能路线第一条）：ssh:// 的 grep 8 路并发
  拉取（RTT 主导：16×50ms 顺序 800ms→~200ms）；本地保持顺序+早停
  （实测 Windows+Defender 串行化文件打开，并发零收益）。
- **稳定性**：调度器 Stop 排空（tick-only 用法的 TempDir 竞态）、
  keepalive 探测看门狗（半开连接判死确定性化）、代理状态条专用锁
  （死锁与竞争双修）。

## v0.7.0 — 输入体验与预算护栏

- **CI 双平台**：GitHub Actions（windows/ubuntu）build/vet/test + wasm
  交叉编译与 Linux -race；首跑即抓出五个真实跨平台缺陷并修复。
- **REPL 行编辑器**：raw 模式下字符插入/退格（整 rune）、←/→/Home/
  End、Ctrl+C 清行/退出——告别 bufio.Scanner 裸读。
- **输入历史（TUI + REPL）**：↑/↓ 召回（连续重复折叠、草稿保存恢
  复、边界不动、封顶 FIFO）；历史跨会话切换保留。
- **括号粘贴**：多行粘贴不再逐行提交——REPL 保留换行（消息可多
  行）、TUI 折叠空格；粘贴内容封顶。
- **TUI Esc 中止**：运行中按 Esc 中断当前回合（不退出应用）；空闲无
  操作；粘贴内保真。
- **每日 token 预算护栏**：当日记账（跨会话/进程共享、午夜翻转）、
  缓存读 0.1 计价、80%/100% 阈值通知（四表面渲染）——软预算不阻断。
- **/perms**：本会话累计的「总是允许」权限规则可见。
- **MCP 资源**：三件套收口——/mcp @server:uri 读取资源文本直接发送。
- **diff 持久化与导出完整性**：工具 diff 随转录 JSONL 持久化（重载不
  丢）；HTML 彩色 diff + thinking 折叠、Markdown diff 围栏。
- **稳定性修复**（含社区）：MCP 资源/粘贴/历史等无界文本全面封顶、
  预算午夜翻转、并行批次完成序确定性、checkpoint 修剪后回滚中止、
  8.3 短名路径。

## v0.6.0 — 终端一等公民与生命周期

- **后台 shell 远程同构**：bash {background:true} 在 ssh:// 工作区走
  不等待的 exec 通道（KillTree 即关通道），remotetest 流式直写 + 断开
  杀整树（Job Object 确定性）。
- **终端会话恢复**：-c/--continue（同工作区最近）与 -r/--resume <前
  缀>（唯一前缀、可跨工作区）三形态共享；/resume 命令运行中切换并回
  放转录（TUI 登记导航锚点）。
- **终端插话**：REPL/TUI 运行中提交的消息经 Steer 入队（下一回合注入
  不打断当前流），与桌面对齐。
- **print 自动化**：--output-format json（逐事件 JSONL，稳定字段名、
  args 原始内嵌）；stdin 管道并入提示词（`git diff | zgo -p`）。
- **层级项目上下文**：~/.zgo/ZGO.md 用户全局 + 自工作区向 git 顶逐层
  择一（浅→深、8KB rune 封顶）；远程工作区经网线读根层（修复静默丢
  失）。
- **会话自动命名**：首回合成功后小额补全生成 ≤40 字符标题（等值守
  卫，手动改名优先，失败保留默认）。
- **会话生命周期钩子**：sessionStart/sessionEnd（观察者）与
  preCompact（非零阻止压缩），外部命令同机制。
- **用量可见性**：/usage 与 zgo usage [前缀]——累计输入/输出/缓存读
  写 + 回合数 + 前缀缓存命中率。
- **导出携带图片**：用户图片块 HTML 内嵌 data URI img、Markdown 链接
  ——自包含分享物不缺图。
- **E2E 远程工作区套件**：ssh:// 会话全栈四场景（往返/jail/bash/后台
  生命周期）；ParseTarget 还原 Windows 盘符根。
- **稳定性修复**（含社区）：OAuth 刷新单飞、系统缓存前缀稳定、
  isAncestor 卷大小写、checkpoint 修剪后回滚中止、remote KillTree 按
  pid 确认、/attach 预算前置拒绝、e2e 等待预算统一。

## v0.5.1 — 回滚三面与 TUI 任务面

- **任意回合回滚**：RewindTo(n) 撤掉第 n..末轮（检查点 seq=n-1 恢复 +
  转录截断，与编辑重发同语义）；桌面每轮用户消息「⟲ 回滚到此」，
  终端 `/rewind [n]`（默认最近一轮）——回滚子系统三面齐备。
- **/export 终端命令**：`/export [md] [路径]` 三端可用（默认自包含
  HTML），与桌面菜单、`zgo sessions export` 同一导出器。
- **桌面任务导航**：Ctrl+↑/↓ 用户回合间跳转（跳离末尾停跟随、目标居中、
  随搜索过滤行走、切会话重置）。
- **TUI 任务面板与回合导航**：todo 工具落盘后 TUI 顶部固定计划块
  （▶/✓/□ 标记、超 6 条折叠、空计划隐藏、/new 清空）；Ctrl+↑/↓ 回合
  跳转 + PageUp/PageDown 半屏滚动（尾随语义与桌面一致，跨 Read 序列
  拼接识别）。
- **zgo sessions search**：跨会话检索 CLI（同一 SearchSessions，表格
  输出：会话前缀/标题/片段）——搜索子系统三面齐备。
- **稳定性修复**：OAuth 刷新单飞（锁内重读，杜绝并发刷新竞态）；系统
  提示缓存前缀稳定（易变尾段拆出，前缀缓存跨回合命中）。

## v0.5.0 — 深化：远程、性能与 CLI 家族


- **SSH 终端**：终端面板在 ssh:// 工作区直接跑远端交互 shell（PTY +
  WindowChange，进程内服务器端到端验证）。
- **终端附件**：print `-i/--image` 与 REPL/TUI `/attach`——附件四表面齐备。
- **跨会话搜索**：流式扫全部转录（文本+工具参数、片段窗口、双上限），
  桌面弹窗点命中切会话并沿用过滤词。
- **惰性会话装载**：启动只读 meta 行（实测 40×320KB 会话全量 ~2s vs
  ~9ms）；首触转录再合并进壳。
- **Anthropic 前缀缓存**：工具表尾/系统提示尾/滚动前缀三处 ephemeral
  断点（≤4），命中入 CacheReadTokens。
- **MCP 设置诊断**：并行探测全部服务器（新实例握手+tools/list，15s 上限），
  设置弹窗结果展示。
- **CLI 运维家族**：`zgo doctor`（环境诊断）、`zgo sessions`（列表/导出/
  删除，唯一前缀）、`zgo memory`（工作区记忆管理）、`zgo models`（目录表
  + 离线校验）——与 `zgo cron` 齐备。
- **E2E 硬化**：并行批次重叠、流中途中止、瞬态重试全栈场景；harness 新增
  ErrorOnce/BlockAfterText 原语。

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
