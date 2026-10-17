package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePage mirrors the boot shape of mygo's generated page (v0.5.5):
// wasm_exec.js loaded first, then the inline script instantiating the app.
const fixturePage = `<!doctype html>
<html lang="en">
  <body>
    <script src="wasm_exec.js"></script>
    <script>
      const go = new Go()
      WebAssembly.instantiateStreaming(fetch("zgo.abc.wasm"), go.importObject)
        .then((r) => go.run(r.instance))
    </script>
  </body>
</html>
`

func TestPatchSiteInjectsBeforeLoader(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "index.html")
	if err := os.WriteFile(page, []byte(fixturePage), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "shim.js")
	if err := os.WriteFile(shim, []byte("SHIM"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchSite(dir, shim); err != nil {
		t.Fatalf("patch: %v", err)
	}

	text, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(string(text), shimTag), strings.Index(string(text), `src="wasm_exec.js"`); i < 0 || j < 0 || i > j {
		t.Fatalf("shim tag missing or after wasm_exec.js:\n%s", text)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "zgo-fs.js")); err != nil || string(got) != "SHIM" {
		t.Fatalf("shim copy = %q, %v", got, err)
	}

	// 幂等：重复 patch（重建后重跑）不重复注入。
	if err := patchSite(dir, shim); err != nil {
		t.Fatalf("re-patch: %v", err)
	}
	text2, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(text2), shimTag); n != 1 {
		t.Fatalf("shim tag injected %d times", n)
	}
}

func TestPatchSiteFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := patchSite(dir, filepath.Join(dir, "shim.js")); err == nil {
		t.Fatal("page without wasm_exec.js accepted")
	}
	if err := patchSite(dir, filepath.Join(dir, "missing.js")); err == nil {
		t.Fatal("missing shim accepted")
	}
}
