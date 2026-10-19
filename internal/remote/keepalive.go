package remote

// SSH 连接保活与惰性重连（issue #220）：长跑桌面会话 + 远程工作区在
// 休眠唤醒/NAT 超时后留下 TCP 半开死连接——不探测则下一个工具调用挂
// 到超时，不重连则此后每次调用都失败到会话结束。
//
// 机制：后台 goroutine 周期发送 keepalive@openssh.com（30s 量级，测试
// 可注入更短间隔）；连续 3 次无回应判死。死连接的下一次操作先尝试用
// 原 Config 重连（对上层透明）；in-flight 调用按现有错误路径干净失
// 败，重试交给模型自纠。连续重连失败 5 次放弃（防风暴）。

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// 保活/重连的哨兵错误。
var (
	errClientClosed    = errors.New("remote client closed")
	errReconnectGaveUp = errors.New("remote reconnect gave up after repeated failures; switch sessions to rebuild")
)

// sftpNewClient/dialTarget/dialUser/dialAuths 是 Dial 的拆出件（重连复
// 用同一套形态，单一事实源）。
func sftpNewClient(conn *ssh.Client) (*sftp.Client, error) {
	return sftp.NewClient(conn)
}

// keepalive 默认参数。
const (
	defaultKeepAliveInterval = 30 * time.Second
	keepAliveMaxFailures     = 3
	reconnectMaxAttempts     = 5
)

// keepAliveState 承载保活与重连状态（原子与互斥并用：alive 是热路径
// 的无锁读，dial/reconnect 串行化）。
type keepAliveState struct {
	interval atomic.Value // time.Duration（SetKeepAliveInterval 注入）
	alive    atomic.Bool
	failures atomic.Int32

	mu       sync.Mutex
	closed   bool // Close 后不再重连
	attempts int  // 连续重连失败计数（风暴防护）

	// probeFn 是探测缝（测试注入：CI 上真实杀连接的时序不可复现——
	// #233 同族教训，判定走确定性注入而非挂钟）。
	probeFn func(deadline time.Duration) error
}

// SetKeepAliveInterval 覆盖探测间隔（测试注入；须在 StartKeepAlive 前）。
func (c *Client) SetKeepAliveInterval(d time.Duration) {
	c.ka.interval.Store(d)
}

// StartKeepAlive 启动保活循环（幂等：重复调用只生效一次）。
func (c *Client) StartKeepAlive() {
	c.kaOn.Do(func() {
		c.ka.alive.Store(true) // 起循环前置位：零值 false 会被先到的 Alive() 读走
		go c.keepAliveLoop()
	})
}

// Alive 报告连接当前是否可用（无锁热路径）。
func (c *Client) Alive() bool { return c.ka.alive.Load() }

// markDead 直接标死（测试注入重连路径用）。
func (c *Client) markDead() { c.ka.alive.Store(false) }

// keepAliveLoop 周期探测；判死后退出循环（重连交给下一次操作的
// ensureConnected）。
func (c *Client) keepAliveLoop() {
	for {
		interval := defaultKeepAliveInterval
		if v, ok := c.ka.interval.Load().(time.Duration); ok && v > 0 {
			interval = v // 每轮重读：SetKeepAliveInterval 迟到也生效
		}
		time.Sleep(interval)
		// 探测带看门狗：SendRequest 无超时——半开连接上写入成功沉入
		// 死 TCP、回应永不到达，失败计数永远凑不齐（Windows CI 上
		// TestKeepaliveDetectsDeadConnection 5s 超时的根因）。回应
		// 等待 2×间隔（下限 250ms）即计一次失败。
		deadline := 2 * interval
		if deadline < 250*time.Millisecond {
			deadline = 250 * time.Millisecond
		}
		probe := c.ka.probeFn
		if probe == nil {
			// 连接快照必须持锁取：probe 的 goroutine 里裸读 c.conn 与
			// ensureConnected 的重连换写（c.conn = conn）数据竞态
			//（ubuntu race 构建曾逮）。快照后探测只碰局部变量。
			c.ka.mu.Lock()
			conn := c.conn
			c.ka.mu.Unlock()
			probe = func(deadline time.Duration) error { return probeConn(conn, deadline) }
		}
		if err := probe(deadline); err == nil {
			c.ka.failures.Store(0)
			continue
		} else if n := c.ka.failures.Add(1); n >= keepAliveMaxFailures {
			c.ka.alive.Store(false)
			return
		}
	}
}

// probeConn 发一次 keepalive 请求并限时等回应（超时视为失败）。conn
// 是调用方持锁取的快照——本函数不碰共享字段（探测 goroutine 的生命
// 周期跨越重连窗口）。
func probeConn(conn *ssh.Client, deadline time.Duration) error {
	type outcome struct{ err error }
	done := make(chan outcome, 1)
	go func() {
		_, _, err := conn.SendRequest("keepalive@openssh.com", true, nil)
		done <- outcome{err}
	}()
	select {
	case r := <-done:
		return r.err
	case <-time.After(deadline):
		return errors.New("keepalive probe timed out")
	}
}

// ensureConnected 在操作前确连接可用：死了先重连（透明），失败计数
// 达上限后原样返回错误（防风暴——用户切会话重建是唯一出路，提示由
// 调用方的错误文案给出）。
func (c *Client) ensureConnected() error {
	if c.ka.alive.Load() {
		return nil
	}
	c.ka.mu.Lock()
	defer c.ka.mu.Unlock()
	if c.ka.closed {
		return errClientClosed
	}
	if c.ka.attempts >= reconnectMaxAttempts {
		return errReconnectGaveUp
	}
	c.ka.attempts++
	conn, err := ssh.Dial("tcp", dialTarget(c.cfg), &ssh.ClientConfig{
		User:            dialUser(c.cfg),
		Auth:            dialAuths(c.cfg),
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
	})
	if err != nil {
		return err
	}
	sftpClient, err := sftpNewClient(conn)
	if err != nil {
		conn.Close()
		return err
	}
	// 换连接：旧 sftp/ssh 关掉（半开连接 Close 的错误无意义）。
	_ = c.closer.Close()
	_ = c.conn.Close()
	c.conn = conn
	c.sftp = sftpClient
	c.closer = sftpClient
	c.ka.attempts = 0
	c.ka.alive.Store(true)
	return nil
}

// markOpSuccess 操作成功即清零重连失败计数（风暴防护只数连续失败）。
func (c *Client) markOpSuccess() { c.ka.attempts = 0 }

// KillConnectionForTesting closes the raw connection without marking
// state — keepalive must discover it by probing (issue #220 test seam).
func (c *Client) KillConnectionForTesting() { _ = c.conn.Close() }
