package remote

import (
	"path"
	"sort"
	"strings"
)

// filepathGlobFS matches a **-aware glob against entries listed by listDir
// (paths relative to the workspace root, "/"-separated), the local Glob
// tool's semantics.
func filepathGlobFS(listDir func(dir string) ([]Entry, error), pattern string) ([]string, error) {
	pattern = strings.TrimPrefix(pattern, "/")
	segs := strings.Split(pattern, "/")
	var matches []string
	var walk func(dir string, segs []string)
	walk = func(dir string, segs []string) {
		if len(segs) == 0 {
			matches = append(matches, dir)
			return
		}
		seg := segs[0]
		rest := segs[1:]
		if seg == "**" {
			// ** 匹配任意层目录（含零层）：零层即本层直接消费 rest，
			// 再对每个子目录保持 ** 递归。
			walk(dir, rest)
			entries, err := listDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.IsDir {
					walk(path.Join(dir, e.Name), segs)
				}
			}
			return
		}
		entries, err := listDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if ok, _ := path.Match(seg, e.Name); ok {
				walk(path.Join(dir, e.Name), rest)
			}
		}
	}
	walk("", segs)
	sort.Strings(matches)
	// 去重（** 的零层分支会重复）。
	dedup := matches[:0]
	var last string
	for _, m := range matches {
		if m != last {
			dedup = append(dedup, m)
			last = m
		}
	}
	return dedup, nil
}
