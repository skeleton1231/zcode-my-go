package tools

import (
	"os"
	"path/filepath"
)

// ReadFileIn reads one workspace file through the path jail — the mention
// expander's single read path, local and SSH-remote alike.
func ReadFileIn(ws Workspace, rel string) ([]byte, error) {
	abs, err := ws.Resolve(rel)
	if err != nil {
		return nil, err
	}
	if ws.IsRemote() {
		return ws.Remote.ReadFile(remoteRel(ws, abs))
	}
	return os.ReadFile(abs)
}

// GlobIn globs a pattern under the workspace root, local or remote.
func GlobIn(ws Workspace, pattern string) ([]string, error) {
	if ws.IsRemote() {
		return ws.Remote.Glob(pattern)
	}
	return filepath.Glob(filepath.Join(ws.Root, pattern))
}
