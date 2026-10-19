package uiapp

// kit 是 uiapp 的视觉套件：内嵌图标（Lucide，ISC 许可，svg 头部自带
// 许可声明）与跨视图复用的小构件。对齐 mygo 官方组件的做法——图标经
// ui.Icon 以文字色绘制、悬停提示走 Tooltip、状态用 Spinner/彩色图标，
// 不再用 emoji/Unicode 字形充当图标（跨平台字形不一致、大小基线参差）。

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

//go:embed icons/*.svg
var iconFS embed.FS

// mustIcon 加载一枚内嵌 SVG；缺失即 panic（编译期资产，缺了是开发错误）。
func mustIcon(name string) *ui.SVG {
	data, err := iconFS.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic("uiapp: missing icon " + name + ": " + err.Error())
	}
	svg, err := ui.ParseSVG(data)
	if err != nil {
		panic("uiapp: bad icon " + name + ": " + err.Error())
	}
	return svg
}

var (
	iconSettings  = mustIcon("settings")
	iconFolder    = mustIcon("folder-tree")
	iconPlus      = mustIcon("plus")
	iconPaperclip = mustIcon("paperclip")
	iconPencil    = mustIcon("pencil")
	iconX         = mustIcon("x")
	iconRefresh   = mustIcon("refresh-cw")
	iconTerminal  = mustIcon("square-terminal")
	iconSearch    = mustIcon("search")
	iconCheck     = mustIcon("check")
	iconCircleX   = mustIcon("circle-x")
	iconSparkles  = mustIcon("sparkles")
	iconSquare    = mustIcon("square")
	iconUndo      = mustIcon("undo-2")
	iconTodo      = mustIcon("list-todo")
	iconClock     = mustIcon("calendar-clock")
	iconBot       = mustIcon("bot")
	iconPlay      = mustIcon("play")
)

// iconButton 造仅图标的按钮：label 供无障碍命名，tooltip 悬停提示。
func iconButton(c *ui.Context, svg *ui.SVG, label, tip string) ui.Element {
	b := ui.Button(c, "").Label(label)
	b.Children(func() { ui.Icon(c, svg).FontSize(16) })
	if tip != "" {
		b.Tooltip(tip)
	}
	return b
}

// textIconButton 造图标+文字的按钮（图标承文字色）。
func textIconButton(c *ui.Context, svg *ui.SVG, label string) ui.Element {
	b := ui.Button(c, "").Label(label)
	b.Children(func() {
		ui.Icon(c, svg).FontSize(14)
		ui.Text(c, label).SingleLine()
	})
	return b
}

// panelHeader 是右侧抽屉（文件树/终端）的统一头部：图标 + 标题 + 右侧动作。
func panelHeader(c *ui.Context, t *ui.Theme, svg *ui.SVG, title string, actions func()) {
	ui.Row(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Icon(c, svg).FontSize(14).TextColor(t.TextMuted)
		ui.Text(c, title).Bold().FontSize(13)
		ui.Spacer(c)
		if actions != nil {
			actions()
		}
	})
}

// closeIconButton 关闭按钮（✕ 的图标版），统一 tooltip。
func closeIconButton(c *ui.Context, onClose func()) ui.Element {
	b := iconButton(c, iconX, "关闭", "关闭")
	if b.Clicked() {
		onClose()
	}
	return b
}

// statusIcon 渲染工具调用的状态图标：运行中 Spinner、失败红 ✕、完成绿 ✓。
func statusIcon(c *ui.Context, t *ui.Theme, item *chatItem) {
	switch {
	case item.running:
		ui.Spinner(c).FontSize(13)
	case item.isError:
		ui.Icon(c, iconCircleX).FontSize(14).TextColor(t.Danger)
	default:
		ui.Icon(c, iconCheck).FontSize(14).TextColor(t.Success)
	}
}
