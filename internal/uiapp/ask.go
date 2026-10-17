package uiapp

import (
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
