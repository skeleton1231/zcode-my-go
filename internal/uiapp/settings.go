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

			ui.Text(c, "协议").FontSize(12)
			ui.Segmented(c, &a.setAPI, "anthropic", "openai", "gemini")

			ui.Text(c, "思考等级").FontSize(12)
			ui.Segmented(c, &a.setThinking, "关", "低", "中", "高")

			ui.TextInput(c, &a.setBaseURL).Label("API Base URL（按协议选择端点）").
				Placeholder("https://api.anthropic.com / https://api.openai.com/v1")

			keyLabel := "API Key"
			if a.settings != nil && a.settings.HasAPIKey {
				keyLabel = "API Key（已配置 " + a.settings.KeyHint + "，留空保持不变）"
			}
			ui.TextInput(c, &a.setKey).Label(keyLabel).Placeholder("sk-…")

			ui.Autocomplete(c, &a.setModel, a.modelSuggestions).Label("模型 ID")

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
