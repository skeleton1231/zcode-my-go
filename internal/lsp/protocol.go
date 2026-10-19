package lsp

// LSP JSON-RPC 分帧客户端（daemon 半边，spec 规则 69）：Content-Length
// 头 + JSON 体的 base protocol；请求/响应按 id 关联，服务端主动请求
// （workspace/configuration、window/workDoneProgress/create 等）必须
// 回应——不回应会让 gopls 握手挂起。通知一律吞掉（诊断走主动拉取，
// 不依赖 publishDiagnostics 推送）。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// rpcMessage 是 LSP wire 上的一条消息（请求/响应/通知三合一形态）。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError 是 JSON-RPC 错误对象（code -32800 是服务端自报 RequestFailed
// ——拉取诊断时 gopls 用它表达「还没算完」，调用方按可重试处理）。
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("lsp rpc %d: %s", e.Code, e.Message) }

// ErrServerBusy 是 gopls 对拉取请求返回 RequestFailed 时的信号（算力
// 暂时不可用，调用方可退回 CLI 子进程路径）。
var ErrServerBusy = errors.New("lsp: server request failed (busy)")

// rpcConn 是单进程的双向连接：写侧串行化，读侧单独 goroutine 分发。
type rpcConn struct {
	w   io.WriteCloser
	r   io.ReadCloser
	mu  sync.Mutex // 写互斥与 id 分配
	seq int64

	pendMu  sync.Mutex
	pending map[string]chan *rpcMessage

	closeOnce sync.Once
	closed    chan struct{}
}

// newRPCConn 接管一对管道。调用方负责进程生命周期。
func newRPCConn(w io.WriteCloser, r io.ReadCloser) *rpcConn {
	return &rpcConn{w: w, r: r, pending: map[string]chan *rpcMessage{}, closed: make(chan struct{})}
}

// start 起读循环（必须在首个请求前调用一次）。
func (c *rpcConn) start() { go c.readLoop() }

// writeFrame 发一条 base protocol 帧（调用方持 c.mu）。
func (c *rpcConn) writeFrame(body []byte) error {
	if _, err := c.w.Write([]byte("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n")); err != nil {
		return err
	}
	_, err := c.w.Write(body)
	return err
}

// call 发请求并等响应。ctx 截断只放弃等待（进程与连接不动——同连接
// 后续请求仍可用；gopls 迟到响应由 id 关联自然丢弃）。
func (c *rpcConn) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.seq >= 1<<53 { // JSON 数字安全上限内循环即可
		c.seq = 0
	}
	c.seq++
	id := strconv.FormatInt(c.seq, 10)
	msg := rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		msg.Params = raw
	}
	body, err := json.Marshal(msg)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	ch := make(chan *rpcMessage, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	err = c.writeFrame(body)
	c.mu.Unlock()
	if err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return errors.New("lsp: connection closed")
	case resp := <-ch:
		if resp.Error != nil {
			if resp.Error.Code == -32800 {
				return ErrServerBusy
			}
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// notify 发通知（无 id，不等回应）。
func (c *rpcConn) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	msg := rpcMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = raw
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.writeFrame(body)
}

// readLoop 解帧分发：响应进 pending 通道；服务端请求回空结果；通知丢弃。
func (c *rpcConn) readLoop() {
	br := bufio.NewReaderSize(c.r, 64<<10)
	for {
		msg, err := readFrame(br)
		if err != nil {
			c.closeOnce.Do(func() { close(c.closed) })
			return
		}
		switch {
		case msg.ID != nil && msg.Method != "": // 服务端主动请求
			// workspace/configuration 等需要非空结果数组；其余空对象
			// 即可。gopls 对未知配置容忍缺省。
			var result any
			if strings.HasPrefix(msg.Method, "workspace/configuration") {
				result = []any{}
			}
			go c.replyServer(msg.ID, result)
		case msg.ID != nil: // 响应
			c.pendMu.Lock()
			ch, ok := c.pending[string(msg.ID)]
			if ok {
				delete(c.pending, string(msg.ID))
			}
			c.pendMu.Unlock()
			if ok {
				ch <- msg
			}
		default: // 通知（publishDiagnostics 等）——诊断走拉取，忽略
		}
	}
}

// replyServer 回应服务端主动请求（空结果/空数组；nil 序列化为显式
// null——JSON-RPC 响应必须带 result 或 error 之一）。
func (c *rpcConn) replyServer(id json.RawMessage, result any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	body, err := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Result: raw})
	if err != nil {
		return
	}
	_ = c.writeFrame(body)
}

// close 关闭管道（幂等）；读循环随 EOF 退出。
func (c *rpcConn) close() {
	c.closeOnce.Do(func() { close(c.closed) })
	_ = c.w.Close()
	_ = c.r.Close()
}

// readFrame 读一帧 base protocol：头区若干行 + 空行 + Content-Length
// 字节体。EOF 干净返回 io.EOF。
func readFrame(br *bufio.Reader) (*rpcMessage, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // 头区结束
		}
		if name, val, ok := strings.Cut(line, ":"); ok {
			if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
				n, err := strconv.Atoi(strings.TrimSpace(val))
				if err != nil || n < 0 || n > 16<<20 {
					return nil, fmt.Errorf("lsp: bad Content-Length %q", val)
				}
				length = n
			}
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: frame without Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("lsp: bad frame body: %w", err)
	}
	return &msg, nil
}
