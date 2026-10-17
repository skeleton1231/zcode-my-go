# DESIGN.md — UI 设计规范

界面用 mygo 的 package `ui` 写成（纯 Go 自绘）。Zgo 的界面为长时间会话、高
信息密度、可读的聊天与工具输出设计。写 UI 前先读本文。

## 产品气质

ZCode 式「冷静、致密、可操作」：键盘优先，不为装饰设计。

- 布局：左侧会话栏（品牌 + 会话列表 + 新建）+ 右侧对话主区 + 底部输入区 +
  状态栏。
- 主题：跟随系统（`c.Theme()` 浅色/深色自动切换），两态都必须是有意设计的。
- 国际化：文案中文为主；变量文本长度不破版（标题截断、宽度上限）。

## 主题与 Token

视觉常量一律取自 `ui.Theme`（`Background / Surface / Border / Text /
TextMuted / Accent / Danger / Warning / Success / Radius / Spacing`），
**禁止硬编码颜色**；确需补充色从 Theme 派生。

- 字体：系统默认 UI 字体；代码与工具输出 `Font("monospace")` + 12pt。
- 字号体系：正文默认（13）、标题 15、欢迎页大标题 30、辅助信息 12 ——
  新增字号须在此登记。

## 组件规范

- 用户消息：右侧气泡（`Radius(12)` + `Surface` 底 + `MaxWidth(520)`），
  文本可选中（`Selectable`）。
- assistant 消息：无气泡直接排版（阅读优先），流式期间 `markdown.Stream`、
  完成后 `markdown.View`，宽度上限 760。
- 工具块：`ui.Collapsible` 折叠卡 —— 头部 `状态符 工具名 · 参数摘要`
  （状态符 ▶ 运行中 / ✓ 完成 / ✕ 出错），展开为等宽输出（`Scroll` +
  `MaxHeight(300)`）。
- 消息列表：`ui.List` + `FollowEnd` + `Smooth` 吸底；空态文案居中。
- 输入区：`TextArea.Lines(1, 8)` 多行自动增高，Enter 发送 /
  Shift+Enter 换行、Up 召回上一条；运行时按钮变为「停止」。
- 侧栏：`ui.Sidebar` + Section「会话」；运行中的会话加 `▶ ` 前缀。
- 弹窗：`ui.Modal`；设置中 API key 永不回显（只提示已配置的尾号）。
- 状态栏：模型名（运行中加 `● `）+ 右对齐 token 用量。

## 禁止

- 绕过 `ui.Theme` 写死颜色或字号。
- 在视图函数外修改状态不经 `win.Update`（测试经 `App.update` 的 nil 窗口路径）。
- 用 spinner 代替即时反馈；流式本身就是反馈。
- 在界面出现任何密钥明文。
