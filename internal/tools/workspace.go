// Package tools implements the built-in agent tools: read, write, edit,
// bash, grep, glob and ls. Every file access goes through Workspace's path
// jail; every output is capped.
package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Workspace is the file access boundary of a session: all tool paths are
// resolved against Root and must stay inside it.
type Workspace struct {
	Root string // absolute, cleaned
}

// Resolve turns a tool path argument into an absolute path inside the
// workspace. Relative paths join Root; absolute paths must already be
// inside. Anything escaping Root is an error, never a redirect.
func (w Workspace) Resolve(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("empty path")
	}
	abs := name
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.Root, name)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(w.Root, abs)
	if err != nil || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", name)
	}
	return abs, nil
}
