package uiapp

import (
	"github.com/egoist/mygo/ui"
)

// settingsModal edits the provider configuration. The API key is only ever
// written, never echoed back; an empty field keeps the stored one.
func (a *App) settingsModal(c *ui.Context, t *ui.Theme) {
	if !a.settingsOpen {
		return
	}
	ui.Modal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(380).Gap(12).Children(func() {
			ui.Text(c, "设置").Bold().FontSize(15)

			ui.TextInput(c, &a.setBaseURL).Label("API Base URL（Anthropic 兼容端点）").
				Placeholder("https://api.anthropic.com")

			keyLabel := "API Key"
			if a.settings != nil && a.settings.HasAPIKey {
				keyLabel = "API Key（已配置 " + a.settings.KeyHint + "，留空保持不变）"
			}
			ui.TextInput(c, &a.setKey).Label(keyLabel).Placeholder("sk-…")

			ui.TextInput(c, &a.setModel).Label("模型 ID").
				Placeholder("claude-sonnet-4-5 / glm-5.2 / …")

			if a.settingsError != "" {
				ui.Text(c, a.settingsError).TextColor(t.Danger).FontSize(12)
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.settingsOpen = false
				}
				if ui.PrimaryButton(c, "保存").
					Disabled(a.saving || a.setModel == "").Clicked() {
					a.saveSettings()
				}
			})
		})
	})
}
