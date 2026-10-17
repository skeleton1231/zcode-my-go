package uiapp

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/slash"

	"github.com/egoist/mygo/ui/markdown"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// View builds the window: a session sidebar, the chat, the composer and a
// status bar. Runs on the main thread for every frame.
func (a *App) View(c *ui.Context) {
	t := c.Theme()
	a.md.Reveal = &markdown.Reveal{Unit: markdown.ByWord}

	ui.Row(c).Fill().Children(func() {
		a.sidebarView(c, t)
		ui.Column(c).Grow(1).Children(func() {
			a.chatView(c, t)
			a.planView(c, t)
			a.composerView(c, t)
			a.statusView(c, t)
		})
	})

	a.renameModal(c, t)
	a.askModal(c, t)
	a.questionModal(c, t)
	a.settingsModal(c, t)
}

// sidebarView lists sessions under the brand, with the actions below.
func (a *App) sidebarView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Width(232).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Padding(14, 16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Zgo").Bold().FontSize(15)
			ui.Spacer(c)
			if ui.Button(c, "设置…").Clicked() {
				a.OpenSettings()
			}
		})
		choice := a.activeID
		sb := ui.Sidebar(c, &choice, func() {
			ui.SidebarSection(c, "会话", nil, func() {
				for _, s := range a.sessions {
					label := s.Title
					if label == "" {
						label = "（未命名）"
					}
					if s.Running {
						label = "▶ " + label
					}
					ui.SidebarItem(c, s.ID, nil, label)
				}
			})
		}).Grow(1)
		if sb.Changed() {
			a.open(choice)
		}
		ui.Row(c).Padding(10, 12).Children(func() {
			if ui.Button(c, "＋ 新会话…").Grow(1).Clicked() {
				a.NewSession()
			}
		})
	})
}

// chatView streams the conversation, following its end.
func (a *App) chatView(c *ui.Context, t *ui.Theme) {
	if a.activeID == "" {
		a.welcomeView(c, t)
		return
	}
	a.searchBarView(c, t)
	a.list.Key = func(i int) any { return i } // append-only rows
	a.list.FollowEnd = !a.searching()
	a.list.FollowAnimation = &ui.Smooth
	// 过滤切换或查询变化时显式滚到过滤列表末尾：FollowEnd 的 following
	// 会因滚动离开而停摆，行数骤减后不重置滚动会让匹配行停在视口外
	// （表现为"1 处"计数正确但列表空白——PR #29 自带测试失败即此因）。
	if key := a.searchKey(); key != a.lastSearchKey {
		a.list.ScrollToEnd()
		a.lastSearchKey = key
	}
	// 搜索时过滤为匹配项；正常视图渲染全部。
	matches := a.SearchMatches()
	rows := a.chat.items
	indexOf := func(row int) int { return row }
	if a.searching() {
		rows = nil
		for _, idx := range matches {
			rows = append(rows, a.chat.items[idx])
		}
		indexOf = func(row int) int { return matches[row] }
	}
	ui.List(c, &a.list, len(rows), func(i int) {
		a.itemView(c, t, indexOf(i), &a.chat.items[indexOf(i)])
	}).Grow(1).Padding(16, 24).Gap(14).Justify(ui.End).Children(func() {
		if len(a.chat.items) == 0 {
			ui.Text(c, "把任务交给 agent：读代码、改文件、跑命令。").TextColor(t.TextMuted).Margin(ui.Auto)
		}
		if a.searching() && len(matches) == 0 {
			ui.Text(c, "没有匹配 \""+a.searchQuery+"\" 的消息").TextColor(t.TextMuted).Margin(ui.Auto)
		}
	})
	if a.chat.endErr != "" {
		// 终止错误以横幅形式钉在消息区底部。
		ui.Row(c).Padding(6, 24).Children(func() {
			ui.Text(c, a.chat.endErr).TextColor(t.Danger).FontSize(12)
		})
	}
}

// searching reports whether the transcript is filtered by a query.
func (a *App) searching() bool {
	return a.searchOpen && strings.TrimSpace(a.searchQuery) != ""
}

// searchKey 是过滤视图的标识：非搜索为空串，搜索时为查询本身。用于
// 检测过滤状态变化并重置列表滚动。
func (a *App) searchKey() string {
	if !a.searching() {
		return ""
	}
	return a.searchQuery
}

// searchBarView filters the transcript (ZCode's in-conversation search).
func (a *App) searchBarView(c *ui.Context, t *ui.Theme) {
	if !a.searchOpen {
		return
	}
	ui.Row(c).Gap(8).Padding(6, 24).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
		AlignItems(ui.Center).Children(func() {
		in := ui.TextInput(c, &a.searchQuery).Placeholder("搜索会话…").Grow(1)
		in.OnTextKey(func(k ui.TextKey) bool {
			if k.Key == ui.KeyEscape {
				a.searchOpen = false
				a.searchQuery = ""
				return true
			}
			return false
		})
		if a.searching() {
			// 计数只统计真正匹配的行；通知恒可见（issue #54）不计入。
			n := 0
			for _, idx := range a.SearchMatches() {
				if a.chat.items[idx].kind != itemNotice {
					n++
				}
			}
			ui.Text(c, fmt.Sprintf("%d 处", n)).TextColor(t.TextMuted).FontSize(12)
		}
		if ui.Button(c, "✕").Clicked() {
			a.searchOpen = false
			a.searchQuery = ""
		}
	})
}

// welcomeView is the empty state before any session is chosen.
func (a *App) welcomeView(c *ui.Context, t *ui.Theme) {
	needsSetup := a.settings != nil && (!a.settings.HasAPIKey || a.settings.Model == "")
	ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
		ui.Text(c, "Zgo").FontSize(30).Bold()
		ui.Text(c, "开源桌面 coding agent · 纯 Go").TextColor(t.TextMuted)
		if needsSetup {
			ui.Text(c, "尚未配置 API Key 或模型：先点「设置…」完成配置。").TextColor(t.Warning)
		}
		if ui.PrimaryButton(c, "选择工作区开始…").Clicked() {
			a.NewSession()
		}
		ui.Text(c, "Enter 发送 · Shift+Enter 换行").TextColor(t.TextMuted).FontSize(12)
	})
}

// itemView renders one chat row: the user's in a bubble, the assistant's as
// Markdown, tool calls as collapsible blocks.
// userItemView renders a user bubble, or its editor while editing.
func (a *App) userItemView(c *ui.Context, t *ui.Theme, i int, item *chatItem) {
	if idx, editing := a.Editing(); editing && idx == i {
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			in := ui.TextArea(c, &a.editDraft).Lines(2, 8).AutoFocus()
			in.OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == 0 && strings.TrimSpace(a.editDraft) != "" {
					a.SubmitEdit()
					k.SetText("")
					return true
				}
				return false
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.CancelEdit()
				}
				if ui.PrimaryButton(c, "重发").Disabled(strings.TrimSpace(a.editDraft) == "").Clicked() {
					a.SubmitEdit()
				}
			})
		})
		return
	}
	ui.Row(c).Children(func() {
		ui.Spacer(c)
		ui.Text(c, item.text).Selectable().Padding(8, 12).Radius(12).
			Background(t.Surface).MaxWidth(520)
		if i == lastUserIndex(a.chat.items) && !a.chat.running && a.activeID != "" && !a.searching() {
			if ui.Button(c, "✎ 编辑").Clicked() {
				a.StartEdit()
			}
		}
	})
}

func (a *App) itemView(c *ui.Context, t *ui.Theme, i int, item *chatItem) {
	switch item.kind {
	case itemUser:
		a.userItemView(c, t, i, item)

	case itemAssistant:
		ui.Column(c).MaxWidth(760).Grow(1).Children(func() {
			if item.think != "" {
				ui.Collapsible(c, "💭 思考过程", &item.thinkOpen, func() {
					ui.Text(c, item.think).FontSize(12).TextColor(t.TextMuted).Selectable()
				})
			}
			if item.streaming {
				markdown.Stream(c, item.text, &a.md)
			} else {
				markdown.View(c, item.text, &a.md)
			}
			if item.errMsg != "" {
				ui.Text(c, item.errMsg).TextColor(t.Danger).FontSize(12)
			}
		})

	case itemTool:
		a.toolView(c, t, item)

	case itemNotice:
		ui.Text(c, item.text).TextColor(t.TextMuted).FontSize(12).
			Padding(4, 10).Radius(8).Background(t.Surface).Margin(ui.Auto)
	}
}

// toolView is a collapsible card: status mark · name · argument summary,
// the full output below when open — a colored diff when the tool carried
// one, else the plain output.
func (a *App) toolView(c *ui.Context, t *ui.Theme, item *chatItem) {
	label := fmt.Sprintf("%s %s · %s", statusMark(item), item.name, summarizeArgs(item.args))
	ui.Column(c).MaxWidth(760).Grow(1).Children(func() {
		ui.Collapsible(c, label, &item.open, func() {
			ui.Scroll(c).MaxHeight(300).Children(func() {
				if item.diff != "" {
					diffView(c, t, item.diff)
					if item.result != "" {
						ui.Text(c, item.result).Font("monospace").FontSize(12).
							TextColor(t.TextMuted).Selectable()
					}
					return
				}
				ui.Text(c, outputOf(item)).Font("monospace").FontSize(12).
					TextColor(t.TextMuted).Selectable()
			})
		})
	})
}

// diffView renders unified-diff lines colored: additions green,
// deletions red, headers and context muted.
func diffView(c *ui.Context, t *ui.Theme, diff string) {
	for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		color := t.TextMuted
		switch {
		case strings.HasPrefix(line, "+"):
			color = t.Success
		case strings.HasPrefix(line, "-"):
			color = t.Danger
		case strings.HasPrefix(line, "@@"):
			color = t.Accent
		}
		ui.Text(c, line).Font("monospace").FontSize(12).TextColor(color).Selectable()
	}
}

func statusMark(item *chatItem) string {
	switch {
	case item.running:
		return "▶"
	case item.isError:
		return "✕"
	default:
		return "✓"
	}
}

func outputOf(item *chatItem) string {
	if item.result != "" {
		return item.result
	}
	if item.running {
		return "运行中…"
	}
	return "（无输出）"
}

// summarizeArgs flattens the tool's JSON arguments to one short line.
func summarizeArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" {
		return ""
	}
	// 常见形状是 {"k":"v",…}：剥掉大括号与引号，截断即可。
	trimmed := strings.TrimSuffix(strings.TrimPrefix(args, "{"), "}")
	trimmed = strings.ReplaceAll(trimmed, "\"", "")
	if len([]rune(trimmed)) > 60 {
		trimmed = string([]rune(trimmed)[:59]) + "…"
	}
	return trimmed
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// planView shows the live todo plan (ZCode's task list panel): a
// collapsible strip above the composer, hidden when the plan is empty.
func (a *App) planView(c *ui.Context, t *ui.Theme) {
	if len(a.plan) == 0 {
		return
	}
	done := 0
	for _, task := range a.plan {
		if task.Status == tools.TodoDone {
			done++
		}
	}
	ui.Row(c).Padding(4, 24).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Collapsible(c, "计划 · "+itoa(done)+"/"+itoa(len(a.plan)), &a.planOpen, func() {
			ui.Column(c).Gap(2).Children(func() {
				for _, task := range a.plan {
					mark := " "
					switch task.Status {
					case tools.TodoInProgress:
						mark = "▶"
					case tools.TodoDone:
						mark = "✓"
					}
					ui.Text(c, mark+" "+task.Content).FontSize(12).
						TextColor(t.TextMuted)
				}
			})
		}).Grow(1)
	})
}

// composerView is the input row: Enter sends, Shift+Enter breaks the line,
// the button stops a run in progress.
func (a *App) composerView(c *ui.Context, t *ui.Theme) {
	a.slashHintsView(c, t)
	a.mentionHintsView(c, t)
	a.attachmentsView(c, t)
	ui.Row(c).Gap(8).Padding(10, 24).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).
		AlignItems(ui.End).Children(func() {
		area := ui.TextArea(c, &a.input).Placeholder("描述任务…（Shift+Enter 换行）").Grow(1).Lines(1, 8)
		area.OnTextKey(func(k ui.TextKey) bool {
			switch {
			case k.Key == ui.KeyEnter && k.Mods == 0:
				if a.activeID != "" && !a.chat.running {
					a.Send()
					k.SetText("")
				}
				return true // 不插入换行
			case k.Key == ui.KeyUp && k.Mods == 0 && k.Text() == "" && a.lastSent != "":
				k.SetText(a.lastSent)
				return true
			}
			return false
		})
		if !a.chat.running {
			if ui.Button(c, "📎").Clicked() {
				a.Attach()
			}
		}
		if a.chat.running {
			if ui.Button(c, "停止").Clicked() {
				a.Stop()
			}
		} else if ui.PrimaryButton(c, "发送").
			Disabled(a.activeID == "" || strings.TrimSpace(a.input) == "").Clicked() {
			a.Send()
		}
	})
}

// attachmentsView shows the composer's pending images as removable chips
// （附件体验：文件名 + ✕，随下一条消息发送）.
func (a *App) attachmentsView(c *ui.Context, t *ui.Theme) {
	if len(a.attach) == 0 {
		return
	}
	ui.Row(c).Gap(6).Padding(6, 24).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).
		Wrap().Children(func() {
		for i, at := range a.attach {
			ui.Row(c).Gap(4).Padding(3, 8).Radius(8).
				Background(t.Surface).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
				ui.Text(c, at.Name).FontSize(12).TextColor(t.TextMuted)
				if ui.Button(c, "✕").Clicked() {
					a.RemoveAttachment(i)
				}
			})
		}
	})
}

// mentionHintsView floats workspace files matching the trailing @token
// （@ 引用提示，slashHints 同款交互）：每帧即时计算，零额外状态；点选
// 把尾部 token 替换为完整路径。
func (a *App) mentionHintsView(c *ui.Context, t *ui.Theme) {
	if a.activeID == "" || a.chat.running || strings.HasPrefix(a.input, "/") {
		return
	}
	prefix := trailingMention(a.input)
	if prefix == "" {
		return
	}
	hints := a.svc.WorkspaceFiles(a.activeID, strings.TrimPrefix(prefix, "@"))
	if len(hints) == 0 || (len(hints) == 1 && hints[0] == prefix) {
		return
	}
	ui.Column(c).Padding(4, 24).Children(func() {
		ui.Column(c).Gap(2).Padding(6, 8).Radius(8).
			Background(t.Surface).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
			for _, h := range hints {
				path := h
				if ui.Button(c, "@"+path).Grow(1).Clicked() {
					a.input = strings.TrimSuffix(a.input, prefix) + path + " "
				}
			}
		})
	})
}

// trailingMention returns the trailing @token being typed, or "".
func trailingMention(input string) string {
	i := strings.LastIndexAny(input, " 	\n")
	tail := input[i+1:]
	if strings.HasPrefix(tail, "@") && len(tail) > 1 && !strings.Contains(tail[1:], "@") {
		return tail
	}
	return ""
}

// slashHintsView floats matching commands above the composer while the
// input is a /-prefix (ZCode's command autocomplete): computed per
// frame, zero extra state; picking a hint fills the composer.
func (a *App) slashHintsView(c *ui.Context, t *ui.Theme) {
	if a.activeID == "" || a.chat.running {
		return
	}
	hints := slash.Match(a.input)
	if len(hints) == 0 {
		return
	}
	ui.Column(c).Padding(4, 24).Children(func() {
		ui.Column(c).Gap(2).Padding(6, 8).Radius(8).
			Background(t.Surface).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
			for _, h := range hints {
				usage := h.Usage
				help := h.Help
				if ui.Button(c, usage+"　—　"+help).Grow(1).Clicked() {
					a.input = strings.SplitN(usage, " ", 2)[0] + " "
				}
			}
		})
	})
}

// statusView shows the model and the run's token usage.
func (a *App) statusView(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Padding(4, 24).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		model := "未配置模型"
		if a.settings != nil && a.settings.Model != "" {
			model = a.settings.Model
		}
		if a.chat.running {
			model = "● " + model
		}
		if a.planMode {
			model = "◍ 计划模式 · " + model
		}
		ui.Text(c, model).TextColor(t.TextMuted).FontSize(12)
		ui.Spacer(c)
		if b := a.budget; b != nil && b.Window > 0 {
			pct := 100 * b.Used / b.Window
			ui.Meter(c, float64(b.Used), 0, float64(b.Window), &ui.MeterLevels{
				Warning:  float64(b.Threshold) * 0.8,
				Critical: float64(b.Threshold),
			}).Label("上下文 " + itoa(pct) + "%").Width(110)
		}
		ui.Text(c, formatUsage(a.chat)).TextColor(t.TextMuted).FontSize(12)
	})
}
