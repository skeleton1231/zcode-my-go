// Zgo is an open-source desktop coding agent: a mygo native-UI shell (pure
// Go, no webview) around the agent core — provider streaming, tool-calling
// loop, JSONL sessions.
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/uiapp"
)

func main() {
	app := mygo.App
	// The name determines the user data directory; set it before anything
	// else resolves paths.
	app.SetName("Zgo")

	dataDir, err := app.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	svc, err := service.New(dataDir)
	if err != nil {
		log.Fatal(err)
	}

	a := uiapp.New(svc)
	app.WhenReady(func() {
		app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "会话", Submenu: []*mygo.MenuItem{
				{Label: "新建会话…", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.NewSession() }},
				{Label: "压缩会话", Accelerator: "CmdOrCtrl+K", Click: func(*mygo.MenuItem, *mygo.Window) { a.Compact() }},
				{Label: "导出会话 HTML…", Accelerator: "CmdOrCtrl+E", Click: func(*mygo.MenuItem, *mygo.Window) { a.Export() }},
				{Label: "切换计划模式（只读）", Accelerator: "CmdOrCtrl+P", Click: func(*mygo.MenuItem, *mygo.Window) {
					if win := a.Window(); win != nil {
						win.Update(func() { a.TogglePlanMode() })
					}
				}},
				{Label: "删除会话", Accelerator: "CmdOrCtrl+D", Click: func(*mygo.MenuItem, *mygo.Window) { a.DeleteActive() }},
				mygo.Separator(),
				{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) {
					if win := a.Window(); win != nil {
						win.Update(func() { a.OpenSettings() })
					}
				}},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		a.Init()
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Zgo",
			Width:     1080,
			Height:    740,
			MinWidth:  760,
			MinHeight: 480,
			Content:   ui.View(a.View),
		})
		a.AttachWindow(win)
		a.InstallUserChannels()
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	svc.Close() // 关停 MCP 子进程
}
