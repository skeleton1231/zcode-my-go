// Package mcp is a Model Context Protocol client over stdio, translated
// from ZCode's core/src/mcp: it launches a server command, performs the
// initialize handshake, and exposes its tools as agent tools.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handshakeTimeout bounds the initialize handshake.
const handshakeTimeout = 20 * time.Second

// Server is one launched MCP server process.
type Server struct {
	Name string
	// Signature identifies the config this server was started from (set
	// by the caller): hot reload compares it to stop-and-restart.
	Signature string
	cmd       *exec.Cmd
	stdin     chan<- []byte
	mu        sync.Mutex
	pending   map[int64]chan json.RawMessage
	nextID    int64
	tools     []ToolDef
	prompts   []PromptDef
	resources []ResourceDef
}

// ToolDef is a tool an MCP server advertises.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Config describes one MCP server to launch, from settings.
type Config struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	// id 按原文保留：规范允许 string|number（issue #18），call 侧不做
	// id 关联（readLoop 已完成投递），int64 硬编码会让字符串 id 的
	// 响应解析失败。
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serverEnv builds the child's environment: Config.Env 覆盖父进程同名
// 变量。先过滤父项再追加——Linux glibc getenv 取第一个匹配，直接 append
// 的覆盖是无效的（Windows last-wins 恰好掩盖了它，issue #18）。
func serverEnv(cmd *exec.Cmd, overrides map[string]string) []string {
	parent := cmd.Environ()
	env := make([]string, 0, len(parent)+len(overrides))
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		if _, overridden := overrides[k]; overridden {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// Start launches a server and performs the MCP handshake, returning its
// advertised tools.
func Start(ctx context.Context, name string, cfg Config) (*Server, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = serverEnv(cmd, cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}

	s := &Server{Name: name, cmd: cmd, pending: map[int64]chan json.RawMessage{}}
	writes := make(chan []byte, 16)
	s.stdin = writes
	go func() {
		for b := range writes {
			if _, err := stdin.Write(b); err != nil {
				return
			}
		}
	}()
	go s.readLoop(bufio.NewReaderSize(stdout, 1024*1024))

	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var init struct {
		Tools []ToolDef `json:"tools"`
	}
	// initialize → (notifications/initialized) → tools/list。initialize
	// 失败必须立即上报：继续 tools/list 只会报误导性的 listing 超时，
	// 掩盖协议版本不匹配等真实原因（issue #18）。
	if err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "zgo", "version": "0.3"},
	}, &init); err != nil {
		s.Close()
		return nil, fmt.Errorf("initializing %s: %w", name, err)
	}
	s.notify("notifications/initialized")
	if err := s.call(ctx, "tools/list", map[string]any{}, &init); err != nil {
		s.Close()
		return nil, fmt.Errorf("listing tools of %s: %w", name, err)
	}
	s.mu.Lock()
	s.tools = init.Tools
	s.mu.Unlock()
	// prompts 是可选能力（spec 规则 28）：不支持/出错的服务器静默无提
	// 示词，不影响启动——tools 才是硬性要求。
	var plist struct {
		Prompts []PromptDef `json:"prompts"`
	}
	if err := s.call(ctx, "prompts/list", map[string]any{}, &plist); err == nil {
		s.mu.Lock()
		s.prompts = plist.Prompts
		s.mu.Unlock()
	}
	// resources 同为可选能力（spec 规则 31）。
	var rlist struct {
		Resources []ResourceDef `json:"resources"`
	}
	if err := s.call(ctx, "resources/list", map[string]any{}, &rlist); err == nil {
		s.mu.Lock()
		s.resources = rlist.Resources
		s.mu.Unlock()
	}
	return s, nil
}

// Tools returns the server's advertised tool definitions.
func (s *Server) Tools() []ToolDef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ToolDef(nil), s.tools...)
}

// Call invokes one tool with raw JSON arguments; the text parts of the
// result are joined.
func (s *Server) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := s.call(ctx, "tools/call", map[string]any{
		"name": name, "arguments": json.RawMessage(args),
	}, &result); err != nil {
		return "", err
	}
	var text string
	for _, part := range result.Content {
		if part.Type == "text" && part.Text != "" {
			if text != "" {
				text += "\n"
			}
			text += part.Text
		}
	}
	if result.IsError {
		return text, fmt.Errorf("tool reported an error: %s", text)
	}
	return text, nil
}

// Close terminates the server process.
func (s *Server) Close() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// call sends a request and decodes the matching response into out.
func (s *Server) call(ctx context.Context, method string, params any, out any) error {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan json.RawMessage, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	select {
	case s.stdin <- append(body, '\n'):
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case raw := <-ch:
		var resp rpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		if resp.Error != nil {
			return fmt.Errorf("%d %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notify sends a notification (no response expected).
func (s *Server) notify(method string) {
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method})
	select {
	case s.stdin <- append(body, '\n'):
	default:
	}
}

// readLoop dispatches responses to their waiters; notifications are
// dropped (v0.3 consumes no server-initiated messages).
func (s *Server) readLoop(r *bufio.Reader) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			s.failPending(err)
			return
		}
		// id 宽容解码（issue #18）：JSON-RPC 规范允许 string|number，
		// 硬编码 int64 会让字符串 id 的响应整行解析失败、静默丢弃，
		// 挂满超时后还报误导性的 listing 错误。
		var head struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(line, &head) != nil || len(head.ID) == 0 || head.Method != "" {
			continue // notification、server-initiated request 或损坏行
		}
		s.mu.Lock()
		ch, ok := s.pending[normalizeID(head.ID)]
		if !ok && len(s.pending) == 1 {
			// id 与任何等待者都对不上（服务器重写了 id，如网关）：恰有
			// 一个等待中的调用时按单飞关联投递，顺序握手下无错配风险。
			for _, c := range s.pending {
				ch, ok = c, true
			}
		}
		s.mu.Unlock()
		if ok {
			ch <- line
		}
	}
}

// normalizeID 把 JSON-RPC id（string 或 number）归一化为 int64；无法
// 解析时返回 0（由调用方走单飞关联兜底）。
func normalizeID(raw json.RawMessage) int64 {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimSuffix(strings.TrimPrefix(s, `"`), `"`)
	id, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return id
}

func (s *Server) failPending(err error) {
	// 进程退出后唤醒所有等待者：投递一个错误响应而不是 close（close 后
	// readLoop 再投递会 panic）。
	fake := json.RawMessage(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"server exited"}}`)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pending {
		select {
		case ch <- fake:
		default:
		}
		delete(s.pending, id)
	}
	_ = err
}

// --- prompts ---------------------------------------------------------------

// PromptDef is one advertised prompt（prompts/list）.
type PromptDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Arguments   []PromptArg `json:"arguments"`
}

// PromptArg is a prompt's declared argument（* 标必填）.
type PromptArg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// PromptMessage is one message of a prompts/get result（role + content
// blocks；只消费 text 块）.
type PromptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// PromptResult is the prompts/get payload.
type PromptResult struct {
	Description string          `json:"description"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptText flattens a prompt's messages to one user-sendable string：
// 文本块按序拼接，assistant 段加角色标注（用户可编辑后再发）。
func (r PromptResult) PromptText() string {
	var b strings.Builder
	for _, m := range r.Messages {
		text := contentText(m.Content)
		if text == "" {
			continue
		}
		if m.Role == "assistant" {
			b.WriteString("[assistant] ")
		}
		b.WriteString(text)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// contentText extracts text from a content block（object 或数组形态）.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if raw[0] == '[' {
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
	} else {
		var one struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &one) != nil {
			return ""
		}
		blocks = append(blocks, one)
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// Prompts returns the server's advertised prompts（握手时探测；未声明
// 能力或出错的服务器为空——能力可选）.
func (s *Server) Prompts() []PromptDef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prompts
}

// GetPrompt expands one prompt with arguments（prompts/get）.
func (s *Server) GetPrompt(ctx context.Context, name string, args map[string]string) (PromptResult, error) {
	var res PromptResult
	if err := s.call(ctx, "prompts/get", map[string]any{
		"name": name, "arguments": args,
	}, &res); err != nil {
		return PromptResult{}, err
	}
	return res, nil
}

// --- resources -------------------------------------------------------------

// ResourceDef is one advertised resource（resources/list）.
type ResourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// ResourceContent is the resources/read payload（text 块可读；blob 类
// 资源 base64，超出本面范围）.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

// Resources returns the server's advertised resources（握手时探测；未
// 声明能力或出错的服务器为空——能力可选）.
func (s *Server) Resources() []ResourceDef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resources
}

// ReadResource reads one resource's text content（resources/read）.
func (s *Server) ReadResource(ctx context.Context, uri string) (ResourceContent, error) {
	var res struct {
		Contents []ResourceContent `json:"contents"`
	}
	if err := s.call(ctx, "resources/read", map[string]any{
		"uri": uri,
	}, &res); err != nil {
		return ResourceContent{}, err
	}
	for _, c := range res.Contents {
		if c.URI == uri {
			return c, nil
		}
	}
	if len(res.Contents) > 0 {
		return res.Contents[0], nil
	}
	return ResourceContent{}, fmt.Errorf("resource %s returned no contents", uri)
}
