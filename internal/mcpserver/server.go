// Package mcpserver 把 zgo 的工具面暴露成 MCP 服务端（stdio JSON-RPC
// 2.0，spec 规则 92）——客户端三面（#126/#188/#248）之外的另一半：任
// 何 MCP 客户端（ZCode、Claude Code、Cursor 等）可以直接调用 zgo 的
// 工具。默认只读面（read/grep/glob/ls）——服务端没有交互确认通道，
// 只读是安全默认；--all 显式开放写/执行面（本地进程、用户亲自起，风
// 险自担）。工具结果过密钥打码（红线同规则 73）。
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/redact"
	"github.com/skeleton1231/zcode-my-go/internal/skills"
)

// ProtocolVersion 与客户端侧一致（2024-11-05）。
const ProtocolVersion = "2024-11-05"

// JSON-RPC 错误码（规范约定段）。
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server serves a fixed tool set over one stdio connection. skills 可
// 选提供 MCP prompts 面（spec 规则 111：技能层作为 prompts/list 与
// prompts/get 暴露——发现名与描述，正文按需取）。
type Server struct {
	tools  []agent.Tool
	skills func() []skills.Skill
	body   func(name string) (string, bool)
	out    io.Writer
}

// New builds a server for the given tools; responses go to out.
func New(tools []agent.Tool, out io.Writer) *Server {
	return &Server{tools: tools, out: out}
}

// WithPrompts 接入 prompts 面：list 返回技能清单，body 按名取正文
// （返回 ok=false 报 unknown prompt）。
func (s *Server) WithPrompts(list func() []skills.Skill, body func(name string) (string, bool)) *Server {
	s.skills, s.body = list, body
	return s
}

// Serve reads newline-delimited JSON-RPC requests until EOF (or ctx ends).
// Notifications (no id) get no response；请求逐条处理——MCP stdio 客户
// 端不并发乱序，串行即正确且免锁。
func (s *Server) Serve(ctx context.Context, r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // 工具参数可达 MB 级
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.write(rpcResponse{JSONRPC: "2.0", Error: &rpcError{
				Code: codeParseError, Message: "parse error: " + err.Error()}})
			continue
		}
		if len(req.ID) == 0 {
			continue // notification：规范不回
		}
		s.dispatch(ctx, req)
	}
	return sc.Err()
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) {
	var result any
	var rerr *rpcError
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "zgo", "version": serverVersion},
		}
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"inputSchema": t.Parameters(),
			})
		}
		result = map[string]any{"tools": list}
	case "tools/call":
		result, rerr = s.call(ctx, req.Params)
	case "prompts/list":
		if s.skills == nil {
			rerr = &rpcError{Code: codeMethodNotFound, Message: "prompts not enabled"}
			break
		}
		list := []map[string]any{}
		for _, sk := range s.skills() {
			desc := sk.Description
			if desc == "" {
				desc = sk.WhenToUse
			}
			list = append(list, map[string]any{"name": sk.Name, "description": desc})
		}
		result = map[string]any{"prompts": list}
	case "prompts/get":
		if s.skills == nil || s.body == nil {
			rerr = &rpcError{Code: codeMethodNotFound, Message: "prompts not enabled"}
			break
		}
		var p struct {
			Name string `json:"name"`
		}
		if len(req.Params) == 0 || json.Unmarshal(req.Params, &p) != nil || p.Name == "" {
			rerr = &rpcError{Code: codeInvalidParams, Message: "prompts/get needs {name}"}
			break
		}
		text, ok := s.body(p.Name)
		if !ok {
			rerr = &rpcError{Code: codeInvalidParams, Message: "unknown prompt: " + p.Name}
			break
		}
		result = map[string]any{
			"description": p.Name,
			"messages": []map[string]any{{
				"role":    "user",
				"content": map[string]any{"type": "text", "text": text},
			}},
		}
	default:
		rerr = &rpcError{Code: codeMethodNotFound,
			Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	s.write(resp)
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) call(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p callParams
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call needs {name, arguments}"}
	}
	for _, t := range s.tools {
		if t.Name() != p.Name {
			continue
		}
		args := p.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		res := t.Execute(ctx, args)
		text := redact.String(res.Content)
		if res.IsError {
			// 工具错误按 MCP 语义是「结果里的 isError」而非协议错误——
			// 客户端会把文本交给它的模型自纠。
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": true,
			}, nil
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}, nil
	}
	return nil, &rpcError{Code: codeInvalidParams,
		Message: fmt.Sprintf("unknown tool: %s", p.Name)}
}

func (s *Server) write(resp rpcResponse) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return // 响应序列化失败无路可报——丢这一条优于拖垮整条连接
	}
	s.out.Write(append(raw, '\n'))
}

// serverVersion 由 CLI 注入（包不依赖 cmd 常量；测试注入任意值）。
var serverVersion = "dev"

// SetVersion 注入版本号（main 启动时调用一次）。
func SetVersion(v string) { serverVersion = v }
