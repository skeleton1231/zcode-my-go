// Command webfs patches the mygo web build (mygo build -platform web) so
// the Zgo browser target gets a file system: it copies web/zgo-fs.js (the
// in-memory fs shim Go's js/wasm syscall layer needs — wasm_exec.js's
// fallback only covers stdout) into the output and loads it before
// wasm_exec.js, whose `if (!globalThis.fs)` keeps a pre-defined one.
//
// Usage, after the web build:
//
//	go tool mygo build -platform web && go run ./cmd/webfs
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	// shimSrc is the checked-in shim; shimTag is the script tag loading it.
	shimSrc = "web/zgo-fs.js"
	shimTag = `<script src="zgo-fs.js"></script>`
)

func main() {
	out := filepath.Join("build", "web")
	idx := filepath.Join(out, "index.html")
	page, err := os.ReadFile(idx)
	if err != nil {
		log.Fatalf("reading %s (run `go tool mygo build -platform web` first): %v", idx, err)
	}
	shim, err := os.ReadFile(shimSrc)
	if err != nil {
		log.Fatalf("reading %s: %v", shimSrc, err)
	}
	if err := os.WriteFile(filepath.Join(out, "zgo-fs.js"), shim, 0o644); err != nil {
		log.Fatalf("writing shim: %v", err)
	}
	// The shim must run before wasm_exec.js; idempotent on re-runs.
	text := string(page)
	if !strings.Contains(text, shimTag) {
		i := strings.Index(text, `wasm_exec.js`)
		if i < 0 {
			log.Fatal("index.html does not reference wasm_exec.js")
		}
		lineStart := strings.LastIndex(text[:i], "<script")
		if lineStart < 0 {
			log.Fatal("no script tag before wasm_exec.js")
		}
		text = text[:lineStart] + shimTag + "\n    " + text[lineStart:]
		if err := os.WriteFile(idx, []byte(text), 0o644); err != nil {
			log.Fatalf("writing index.html: %v", err)
		}
	}
	fmt.Println("webfs: zgo-fs.js 已注入", idx)
}
