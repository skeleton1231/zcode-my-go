package uiapp

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// 跨会话搜索（ZCode task-index 的轻量落地）：弹窗输入关键词，后台流式
// 扫全部会话的转录，结果按会话列出；点一条切到该会话并沿用为会话内
// 过滤词。扫描在后台 goroutine，取消时丢弃旧结果（代次标记）。
type crossSearchState struct {
	open    bool
	query   string
	running bool
	gen     uint64 // 代次：旧扫描结果到达时丢弃
	results []crossHit
	err     string
}

type crossHit struct {
	SessionID string
	Title     string
	Snippet   string
}

// OpenCrossSearch toggles the cross-session search modal.
func (a *App) OpenCrossSearch() {
	a.cross.open = !a.cross.open
	if a.cross.open {
		a.cross.query, a.cross.results, a.cross.err = "", nil, ""
	} else {
		a.cross.gen++ // 在途扫描作废
	}
}

// RunCrossSearch fires the background scan for the typed query.
func (a *App) RunCrossSearch() {
	q := strings.TrimSpace(a.cross.query)
	if q == "" {
		a.cross.results, a.cross.running, a.cross.err = nil, false, ""
		return
	}
	a.cross.running = true
	a.cross.results, a.cross.err = nil, ""
	a.cross.gen++
	gen := a.cross.gen
	id := a.activeID // 记住发起会话：命中自己时排序提示仍可用
	go func() {
		hits := a.svc.SearchSessions(q)
		a.update(func() {
			if a.cross.gen != gen {
				return // 旧扫描：丢弃
			}
			a.cross.running = false
			a.cross.results = make([]crossHit, len(hits))
			for i, h := range hits {
				a.cross.results[i] = crossHit{SessionID: h.SessionID,
					Title: h.Title, Snippet: h.Snippet}
			}
			if len(hits) == 0 {
				a.cross.err = "没有匹配的会话"
			}
		})
		_ = id
	}()
}

// crossSearchModal renders the modal: input row + result list.
func (a *App) crossSearchModal(c *ui.Context, t *ui.Theme) {
	if !a.cross.open {
		return
	}
	ui.Modal(c, &a.cross.open, func() {
		ui.Column(c).Width(560).Gap(10).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconSearch).FontSize(15).TextColor(t.TextMuted)
				ui.Text(c, "搜索所有会话").Bold().FontSize(15)
			})
			ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
				ui.TextInput(c, &a.cross.query).Grow(1).
					Placeholder("关键词（消息与工具参数）")
				if ui.PrimaryButton(c, "搜索").Disabled(a.cross.running).Clicked() {
					a.RunCrossSearch()
				}
			})
			if a.cross.running {
				ui.Spinner(c).FontSize(12)
				ui.Text(c, "扫描中…").TextColor(t.TextMuted).FontSize(12)
			}
			if a.cross.err != "" {
				ui.Text(c, a.cross.err).TextColor(t.TextMuted).FontSize(12)
			}
			ui.Scroll(c).MaxHeight(360).Children(func() {
				for _, h := range a.cross.results {
					hit := h
					title := hit.Title
					if title == "" {
						title = "（未命名）"
					}
					if ui.Button(c, title+" — "+hit.Snippet).Grow(1).Clicked() {
						a.cross.open = false
						a.cross.gen++
						a.OpenSession(hit.SessionID)
						a.searchOpen = true
						a.searchQuery = strings.TrimSpace(a.cross.query)
					}
				}
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "关闭").Clicked() {
					a.OpenCrossSearch()
				}
			})
		})
	})
}
