package service

// 会话保留期归档（spec 规则 98）：超过 N 天未活动的会话在启动时从
// sessions/ 移入 archive/——不删除（转录是用户数据，恢复一条命令），
// 只是从活跃面收敛（list/-c/搜索不再看到）。mtime 是活动判据（每次
// 追加都 touch），元数据行之外零解析——归档是纯文件操作。

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sweepStaleSessions 把 sessionsDir 里 mtime 早于 keepDays 天前的
// *.jsonl 移入 archiveDir（重命名，原子）；keepDays<=0 关闭。返回归
// 档数。坏目录/坏文件跳过（保留期是整理，不是门禁——单个失败不该
// 阻止启动）。
func sweepStaleSessions(sessionsDir, archiveDir string, keepDays int) int {
	if keepDays <= 0 {
		return 0
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	moved := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.MkdirAll(archiveDir, 0o700); err != nil {
			return moved
		}
		dst := filepath.Join(archiveDir, e.Name())
		// 同名冲突（跨多次归档周期）不覆盖——保新弃旧。
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if os.Rename(filepath.Join(sessionsDir, e.Name()), dst) == nil {
			moved++
		}
	}
	return moved
}

// ArchivedSessions 列出 archive/ 里的会话文件名（新到旧）。
func (s *AgentService) ArchivedSessions() []string {
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(s.path), "archive"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, e.Name())
		}
	}
	return out
}

// RestoreArchivedSession 把 archive/ 的一个会话文件移回 sessions/
//（spec 规则 98 的恢复面：name 是文件名或去扩展名形态）。sessions/
// 已有同名时拒绝（不覆盖活跃会话）。
func (s *AgentService) RestoreArchivedSession(name string) error {
	if !strings.HasSuffix(name, ".jsonl") {
		name += ".jsonl"
	}
	dataDir := filepath.Dir(s.path)
	src := filepath.Join(dataDir, "archive", name)
	if _, err := os.Stat(src); err != nil {
		return err
	}
	dst := filepath.Join(dataDir, "sessions", name)
	if _, err := os.Stat(dst); err == nil {
		return os.ErrExist
	}
	// 恢复即回到活跃面：mtime 刷新到当下（否则下次启动又被归档）。
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(dst, now, now)
}
