# DESIGN.md — UI 设计规范

前端（`frontend/`）是 Zgo 的操作界面：长时间会话、高信息密度、可读的聊天与
工具输出。写 UI 前先读本文。

## 产品气质

ZCode 式「冷静、致密、可操作」：为长会话和键盘流设计，不为装饰设计。

- 布局：左侧会话栏（可折叠）+ 右侧对话主区 + 底部输入区。
- 主题：浅色 / 深色双主题，跟随系统，手动可切换；两主题都必须是有意设计的。
- 国际化：文案用中文为主（v0.1），结构上不硬编码像素宽度，变量文本长度不破版。

## 设计 Token

全部视觉常量走 CSS 自定义属性（`frontend/src/styles/tokens.css`），禁止散落
硬编码：

```css
:root {
  --color-surface / --color-surface-raised / --color-border
  --color-text / --color-text-muted / --color-accent
  --color-ok / --color-warn / --color-danger
  --text-ui-sm / --text-ui-base / --text-ui-lg（UI 字号只用这一档体系）
  --space-1 … --space-8（4px 基准的间距阶梯）
  --radius-sm / --radius-md / --radius-lg
  --duration-fast / --duration-normal + --ease-out-expo
}
```

- 字体：系统 UI 栈（`system-ui` 等）+ 代码用 `ui-monospace` 栈；不引入 webfont。
- 动效只动 `transform / opacity`；流式文本更新不做整列重排动画。

## 组件规范

- 消息气泡：user 右侧强调底色；assistant 无气泡直接排版（阅读优先）。
- 工具块：折叠卡片 —— 头部 `toolName · 参数摘要 · 状态点`，展开显示完整输出；
  输出为 `<pre>` 保留空白，超长内部滚动。
- 状态点语义：运行中（accent，呼吸动画）/ 成功（ok）/ 错误（danger）。
- 输入区：多行自动增高，Enter 发送 / Shift+Enter 换行；运行时按钮变为 Stop。
- 焦点可见（`:focus-visible` 有明确样式），键盘可用 Tab 顺序通过。

## 禁止

- 引入组件库默认样式充当设计（v0.1 无 UI 依赖，手写 CSS）。
- 用 Tailwind 内建字号体系混入 `--text-ui-*`。
- 整页 loading spinner 代替骨架/占位。
- 在 UI 中出现任何密钥明文（输入框 password 类型，仅设置面板）。
