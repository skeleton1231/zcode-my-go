package uiapp

// theme.go：Zgo 的视觉基调。不整份覆盖系统主题（暗/亮与桌面强调色由
// mygo 跟随系统，这是官方观感的一部分），只做两件事：
//  1. 每帧把窗口主题的语义色接进 markdown 排版（链接用强调色、次要文字
//     用 TextMuted），系统外观切换即时生效；
//  2. 提供卡片/浮层的统一样式基准（表面色 + 边框 + 圆角 + 轻阴影）。

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// applyMarkdownTheme 把窗口主题接入 markdown.Options（零值字段本就回落
// 到 markdown.DefaultTheme，这里只覆盖要跟帧走的颜色与排版密度）。
func applyMarkdownTheme(o *markdown.Options, t *ui.Theme) {
	o.Theme = &markdown.Theme{
		LineHeight: 1.6,
		Link:       t.Accent,
		Muted:      t.TextMuted,
	}
}

// hintCard 是输入区上方提示浮层（slash/mention）的容器样式：表面色卡片
// + 边框 + 圆角 + 轻阴影，读作"浮在输入区上"而不是多出来的一块布局。
func hintCard(el ui.Element, t *ui.Theme) ui.Element {
	return el.Background(t.Surface).
		BorderWidth(1, 1, 1, 1).BorderColor(t.Border).
		Radius(t.Radius).
		Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.08))
}
