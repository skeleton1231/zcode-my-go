package service

import (
	"sort"
	"sync"

	"github.com/skeleton1231/zcode-my-go/internal/mcp"
)

// MCPDiagnostics launches fresh probes of every configured MCP server
// （并行，互不拖累），never touching the live set — the settings panel's
// 「诊断 MCP」.
func (s *AgentService) MCPDiagnostics() []mcp.Report {
	s.mu.Lock()
	servers := s.settings.MCPServers
	s.mu.Unlock()
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	reports := make([]mcp.Report, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string, cfg mcp.Config) {
			defer wg.Done()
			reports[i] = mcp.Diagnose(name, cfg)
		}(i, name, servers[name])
	}
	wg.Wait()
	return reports
}
