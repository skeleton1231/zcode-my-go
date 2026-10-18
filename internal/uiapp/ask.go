package uiapp

import (
	"log"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

// askModal confirms a tool call the permission system flagged (ZCode's
// permission center, desktop side): allow once, always this session, or
// deny with a note for the model.
func (a *App) askModal(c *ui.Context, t *ui.Theme) {
	if !a.askOpen {
		return
	}
	ui.Modal(c, &a.askOpen, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "确认工具调用").Bold().FontSize(15)
			ui.Text(c, a.askReq.Tool).Font("monospace").TextColor(t.Text)
			if a.askReq.Args != "" {
				ui.Text(c, a.askReq.Args).Font("monospace").FontSize(12).
					TextColor(t.TextMuted).Selectable()
			}
			ui.Text(c, "风险："+riskLabel(a.askReq.Risk)+" · "+a.askReq.Reason).
				TextColor(t.TextMuted).FontSize(12)
			if a.askReq.Diff != "" {
				ui.Text(c, "将做出的改动：").FontSize(12)
				ui.Scroll(c).MaxHeight(260).Children(func() {
					diffView(c, t, a.askReq.Diff)
				})
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "拒绝").Clicked() {
					a.answerAsk(agent.AskResponse{Deny: true, Note: "用户在确认框选择了拒绝"})
				}
				if ui.Button(c, "本会话总是允许").Clicked() {
					a.answerAsk(agent.AskResponse{Always: true})
				}
				if ui.PrimaryButton(c, "允许一次").Clicked() {
					a.answerAsk(agent.AskResponse{})
				}
			})
		})
	})
}

// questionModal delivers the ask tool's question and collects an answer.
func (a *App) questionModal(c *ui.Context, t *ui.Theme) {
	if !a.qOpen {
		return
	}
	ui.Modal(c, &a.qOpen, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "Agent 提问").Bold().FontSize(15)
			ui.Text(c, a.qText.Text).Selectable()

			if len(a.qText.Options) > 0 {
				ui.Column(c).Gap(6).Children(func() {
					for _, option := range a.qText.Options {
						if ui.Button(c, option).Grow(1).Clicked() {
							a.answerQuestion(option)
						}
					}
				})
			}

			in := ui.TextInput(c, &a.qAnswer).Placeholder("或输入回答…").Grow(1)
			if in.Submitted() {
				a.answerQuestion(a.qAnswer)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "跳过").Clicked() {
					a.answerQuestion("")
				}
				if ui.PrimaryButton(c, "回答").Disabled(a.qAnswer == "").Clicked() {
					a.answerQuestion(a.qAnswer)
				}
			})
		})
	})
}

func riskLabel(risk permission.RiskLevel) string {
	switch risk {
	case "low":
		return "低"
	case "high":
		return "高"
	default:
		return "中"
	}
}

// renameModal renames the active session (ZCode's rename).
func (a *App) renameModal(c *ui.Context, t *ui.Theme) {
	if !a.renameOpen {
		return
	}
	ui.Modal(c, &a.renameOpen, func() {
		ui.Column(c).Width(380).Gap(12).Children(func() {
			ui.Text(c, "重命名会话").Bold().FontSize(15)
			in := ui.TextInput(c, &a.renameDraft).AutoFocus()
			if in.Submitted() {
				a.SubmitRename()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.renameOpen = false
				}
				if ui.PrimaryButton(c, "保存").Disabled(strings.TrimSpace(a.renameDraft) == "").Clicked() {
					a.SubmitRename()
				}
			})
		})
	})
}

// trustModal 首开未授信工作区的信任确认（spec 规则 53 的桌面半边）：
// 授信即加载其 .zgorules.json 与 .zgo 命令/技能层；暂不则仅全局层，
// 之后可经 /trust 授信。
func (a *App) trustModal(c *ui.Context, t *ui.Theme) {
	if !a.trustOpen {
		return
	}
	ui.Modal(c, &a.trustOpen, func() {
		ui.Column(c).Width(440).Gap(12).Children(func() {
			ui.Text(c, "信任此工作区？").Bold().FontSize(15)
			ui.Text(c, a.trustWS).Font("monospace").FontSize(12).
				TextColor(t.TextMuted).Selectable()
			ui.Text(c, "将加载该工作区的 .zgorules.json 权限规则与 .zgo 命令/技能层。\n仅对你审阅过的仓库授信——恶意仓库可借规则文件给自己放权。").
				FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "暂不").Clicked() {
					a.trustOpen = false
				}
				if ui.Button(c, "信任并加载").Bold().Clicked() {
					a.trustOpen = false
					ws := a.trustWS
					go func() {
						if err := a.svc.TrustWorkspace(ws, "desktop"); err != nil {
							log.Printf("trusting %s: %v", ws, err)
						}
					}()
				}
			})
		})
	})
}
