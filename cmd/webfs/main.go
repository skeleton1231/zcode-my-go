// Command webfs patches the mygo web build (mygo build -platform web) so
// the Zgo browser target gets a file system: it copies web/zgo-fs.js (the
// in-memory fs shim Go's js/wasm syscall layer needs — wasm_exec.js's
// fallback only covers stdout) into the output and loads it before
// wasm_exec.js, whose `if (!globalThis.fs)` keeps a pre-defined one. The
// shim also persists every file change to IndexedDB and restores them
// before the wasm instantiates (it wraps WebAssembly.instantiateStreaming),
// so a reload keeps settings, sessions and workspace files.
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
	if err := patchSite(filepath.Join("build", "web"), shimSrc); err != nil {
		log.Fatal(err)
	}
	fmt.Println("webfs: zgo-fs.js 已注入 build/web")
}

// patchSite copies the shim into outDir and loads it from index.html
// before wasm_exec.js. Idempotent: re-running changes nothing.
func patchSite(outDir, shim string) error {
	idx := filepath.Join(outDir, "index.html")
	page, err := os.ReadFile(idx)
	if err != nil {
		return fmt.Errorf("reading %s (run `go tool mygo build -platform web` first): %w", idx, err)
	}
	data, err := os.ReadFile(shim)
	if err != nil {
		return fmt.Errorf("reading %s: %w", shim, err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "zgo-fs.js"), data, 0o644); err != nil {
		return fmt.Errorf("writing shim: %w", err)
	}
	text := string(page)
	if strings.Contains(text, shimTag) {
		return nil // 已注入（重复构建后重跑）
	}
	i := strings.Index(text, `wasm_exec.js`)
	if i < 0 {
		return fmt.Errorf("index.html does not reference wasm_exec.js")
	}
	lineStart := strings.LastIndex(text[:i], "<script")
	if lineStart < 0 {
		return fmt.Errorf("no script tag before wasm_exec.js")
	}
	text = text[:lineStart] + shimTag + "\n    " + text[lineStart:]
	if err := os.WriteFile(idx, []byte(text), 0o644); err != nil {
		return fmt.Errorf("writing index.html: %w", err)
	}
	return nil
}
