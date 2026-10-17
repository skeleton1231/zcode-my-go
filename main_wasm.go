//go:build js

package main

import (
	"os"

	"github.com/egoist/mygo"
)

// init runs before main: browsers define neither $HOME nor
// $XDG_CONFIG_HOME, so mygo's default PathUserData (os.UserConfigDir)
// errors and main's log.Fatal kills the app on load. Route user data to
// wasm's in-memory file system instead: settings and sessions live for the
// page's lifetime (a persistence bridge — localStorage/IndexedDB — is the
// next part of the wasm roadmap).
func init() {
	const dir = "/zgo-data"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic(err)
	}
	mygo.App.SetPath(mygo.PathUserData, dir)
}
