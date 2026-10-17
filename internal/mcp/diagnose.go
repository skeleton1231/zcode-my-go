package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Diag bounds one server's whole diagnostic run（启动 + 握手 + tools/list）
// ——诊断不能被挂死的服务器拖住界面。
const (
	diagTimeout = 15 * time.Second
	diagToolCap = 12
)

// Report is one MCP server's diagnostic outcome（设置面板的「诊断 MCP」）.
type Report struct {
	Name      string
	OK        bool
	Error     string
	Tools     []string
	LatencyMS int64
}

// Diagnose launches a FRESH instance of the configured server, waits for
// its initialize handshake and tools/list to land, and shuts it down —
// never touching the live set. Tool names are capped for display.
func Diagnose(name string, cfg Config) Report {
	start := time.Now()
	rep := Report{Name: name}
	ctx, cancel := context.WithTimeout(context.Background(), diagTimeout)
	defer cancel()
	srv, err := Start(ctx, name, cfg)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	defer srv.Close()

	// Tools() 在握手完成后异步填充：轮询至非空或超时（挂死/握手失败
	// 的服务器在这里给出可读结论）。
	tools := srv.Tools()
	deadline := time.Now().Add(8 * time.Second)
	for len(tools) == 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			tools = srv.Tools()
		case <-time.After(50 * time.Millisecond):
			tools = srv.Tools()
		}
		if len(tools) > 0 {
			break
		}
	}
	rep.LatencyMS = time.Since(start).Milliseconds()
	if len(tools) == 0 {
		rep.Error = fmt.Sprintf("无工具或握手未完成（%dms 内）", diagTimeout.Milliseconds()/2)
		return rep
	}
	rep.OK = true
	for i, t := range tools {
		if i >= diagToolCap {
			rep.Tools = append(rep.Tools, "…")
			break
		}
		rep.Tools = append(rep.Tools, t.Name)
	}
	return rep
}

// Line renders one report for a terminal or settings line.
func (r Report) Line() string {
	if !r.OK {
		return r.Name + "：✕ " + r.Error
	}
	return fmt.Sprintf("%s：✓ %d 个工具（%s）", r.Name, len(r.Tools), strings.Join(r.Tools, ", "))
}
