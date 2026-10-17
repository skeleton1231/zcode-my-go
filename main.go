// Zgo is an open-source desktop coding agent: a mygo shell around a Go
// agent core (provider streaming, tool-calling loop) with a React frontend.
package main

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/skeleton1231/zcode-my-go/internal/service"
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
	agentService, err := service.New(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	mygo.BindAs("Agent", agentService)

	app.WhenReady(func() {
		app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "Zgo",
			Width:           1180,
			Height:          780,
			MinWidth:        900,
			MinHeight:       600,
			BackgroundColor: "light-dark(#f6f6f7, #17171a)",
			URL:             "/",
		})
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
