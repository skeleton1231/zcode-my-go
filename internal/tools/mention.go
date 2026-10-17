package tools

import (
	"os"
	"path/filepath"
	"sort"
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

// ListIn lists one workspace directory (local or remote), directories
// first then names — the file-tree panel's data.
func ListIn(ws Workspace, rel string) ([]DirEntry, error) {
	abs := ws.Root
	if rel != "" {
		resolved, err := ws.Resolve(rel)
		if err != nil {
			return nil, err
		}
		abs = resolved
	}
	var entries []DirEntry
	if ws.IsRemote() {
		list, err := ws.Remote.ReadDir(remoteRel(ws, abs))
		if err != nil {
			return nil, err
		}
		entries = list
	} else {
		dirs, err := os.ReadDir(abs)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			entries = append(entries, DirEntry{Name: d.Name(), IsDir: d.IsDir()})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// GlobIn globs a pattern under the workspace root, local or remote.
func GlobIn(ws Workspace, pattern string) ([]string, error) {
	if ws.IsRemote() {
		return ws.Remote.Glob(pattern)
	}
	return filepath.Glob(filepath.Join(ws.Root, pattern))
}
