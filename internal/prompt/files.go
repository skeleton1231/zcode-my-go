package prompt

import (
	"io"
	"os"
	"path/filepath"
)

// joinPath is filepath.Join, kept local for symmetric test stubbing.
func joinPath(dir, name string) string { return filepath.Join(dir, name) }

// readFileLimited reads at most limit bytes of a file.
func readFileLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(limit)))
}
