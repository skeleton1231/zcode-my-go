package remote

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// TestKeepaliveMarksDeadAfterFailures: keepalive 循环连续 3 次探测失
// 败即标记死连接（issue #220 判死语义）。确定性注入探测缝——CI 上真
// 实杀连接的时序不可复现（Windows 上关闭后的 SendRequest 行为不稳
// 定，挂钟断言连续两轮 flake；#233 同族教训）。
func TestKeepaliveMarksDeadAfterFailures(t *testing.T) {
	c := &Client{}
	c.SetKeepAliveInterval(10 * time.Millisecond)
	var calls int32
	c.ka.probeFn = func(time.Duration) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("probe failed")
	}
	c.StartKeepAlive()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !c.Alive() {
			if atomic.LoadInt32(&calls) < 3 {
				t.Fatalf("marked dead after only %d probes", calls)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("keepalive never marked the dead connection")
}

// TestReconnectAfterDeath: 死连接的下一次调用自动重连——对上层透明
// （issue #220：此后每一个工具调用失败到会话结束是稳定性短板）。
func TestReconnectAfterDeath(t *testing.T) {
	srv := remotetest.Start(t)
	host, port, _ := splitHostPort(srv.Address)
	c, err := Dial(Config{
		User: "tester", Host: host, Port: port,
		Key: srv.KeyPEM, Root: srv.Root,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetKeepAliveInterval(50 * time.Millisecond)
	c.StartKeepAlive()

	// 正常调用先通。
	res, err := c.Exec("echo alive")
	if err != nil || !strings.Contains(res.Output, "alive") {
		t.Fatalf("pre-death exec: %v %q", err, res.Output)
	}

	// 模拟连接死亡（标死但不关服务端——重连有处可去）。
	c.markDead()

	// 下一次调用透明重连。
	res, err = c.Exec("echo reborn")
	if err != nil || !strings.Contains(res.Output, "reborn") {
		t.Fatalf("post-death exec (should have reconnected): %v %q", err, res.Output)
	}
	if !c.Alive() {
		t.Fatal("client not alive after reconnect")
	}
}
