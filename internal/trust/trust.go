// Package trust 记录用户显式授信的工作区（pi project-trust / CC 信任
// 提示的 Zgo 落地，spec 规则 53）：未授信工作区的 .zgorules.json 与工
// 作区层命令/技能不加载——克隆来的恶意仓库不能靠自带规则文件给自己
// 放权（allow 规则即提权面）。
package trust

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 是一条授信记录。
type Entry struct {
	Path      string    `json:"path"` // 授信时的规范化绝对路径
	GrantedAt time.Time `json:"grantedAt"`
	Via       string    `json:"via,omitempty"` // cli | slash | session
}

// Store 是授信表（dataDir/trusted.json，原子写；并发安全）。
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore 构建存储。
func NewStore(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, "trusted.json")}
}

// Normalize 规范化工作区路径（键形态：Clean；Windows 路径大小写不敏
// 感——盘符路径整体小写归一，D:/WS 与 d:\ws 是同一键）。
func Normalize(p string) string {
	p = filepath.Clean(p)
	if len(p) >= 2 && p[1] == ':' {
		return strings.ToLower(p)
	}
	return p
}

// IsTrusted 报告路径是否已授信（读失败视为未授信——保守）。
func (s *Store) IsTrusted(workspace string) bool {
	entries, err := s.load()
	if err != nil {
		return false
	}
	_, ok := entries[Normalize(workspace)]
	return ok
}

// Grant 授信一个路径（幂等，刷新时间戳）。
func (s *Store) Grant(workspace, via string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return err
	}
	entries[Normalize(workspace)] = Entry{Path: Normalize(workspace),
		GrantedAt: time.Now(), Via: via}
	return s.save(entries)
}

// Revoke 撤销授信（幂等）。
func (s *Store) Revoke(workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return err
	}
	delete(entries, Normalize(workspace))
	return s.save(entries)
}

// List 列出授信（按路径排序）。
func (s *Store) List() []Entry {
	entries, err := s.load()
	if err != nil {
		return nil
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// load 读全表（损坏即空表重起——授信表丢失最坏是重新授信提示）。
func (s *Store) load() (map[string]Entry, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Entry{}, nil
		}
		return nil, err
	}
	var entries map[string]Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return map[string]Entry{}, nil
	}
	return entries, nil
}

// save 原子落盘。
func (s *Store) save(entries map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing trust store: %w", err)
	}
	return os.Rename(tmp, s.path)
}
