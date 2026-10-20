package service

// 附加工作目录（spec 规则 128，/add-dir 的服务面）：把额外目录并入会
// 话的 path jail（Workspace.Extra）——跨仓/monorepo 邻目录工作流。显
// 式用户动作即本会话对该目录的授权（与授信门同源的同意语义）；远程
// 会话 v1 不支持（Remote 单根）。会话作用域，不落盘（重启即回单根
// ——spec 注明）。

import (
	"fmt"
	"path/filepath"
)

// maxExtraDirs 附加目录上限（防无上限扩权面——一次会话不该挂一堆根）。
const maxExtraDirs = 8

// AddWorkspaceDir 把 dir 并入会话的 jail（幂等；Root 本身与既有条目去
// 重）。返回并入后的清单。
func (s *AgentService) AddWorkspaceDir(sessionID, dir string) ([]string, error) {
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return nil, fmt.Errorf("add-dir: %w", err)
	}
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("add-dir: 会话 %s 不存在", sessionID)
	}
	// wsCache 惰性填充（首次工具使用才拨号/建缓存）：先确保该会话的
	// Workspace 已入缓存。workspaceFor 自管锁序——不能在 s.mu 内调。
	s.workspaceFor(sessionID, sess.Info().Workspace)

	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.wsCache[sessionID]
	if !ok {
		return nil, fmt.Errorf("add-dir: 工作区未就绪")
	}
	if ws.IsRemote() {
		return nil, fmt.Errorf("add-dir: SSH 工作区 v1 不支持附加目录")
	}
	if abs == ws.Root {
		return ws.Extra, nil
	}
	for _, e := range ws.Extra {
		if e == abs {
			return ws.Extra, nil
		}
	}
	if len(ws.Extra) >= maxExtraDirs {
		return nil, fmt.Errorf("add-dir: 附加目录已达上限 %d", maxExtraDirs)
	}
	ws.Extra = append(ws.Extra, abs)
	s.wsCache[sessionID] = ws
	return ws.Extra, nil
}

// WorkspaceDirs 报告会话当前的根清单（Root + Extra；远程只有 Root）。
func (s *AgentService) WorkspaceDirs(sessionID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.wsCache[sessionID]
	if !ok {
		return nil
	}
	out := []string{ws.Root}
	return append(out, ws.Extra...)
}
