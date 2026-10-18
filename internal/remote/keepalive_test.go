package remote

import (
	"strings"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/remotetest"
)

// TestKeepaliveDetectsDeadConnection: keepalive 循环探测服务端断开——
// 周期内标记死连接（issue #220：死连接不被发现，下一个工具调用挂到
// 超时）。
func TestKeepaliveDetectsDeadConnection(t *testing.T) {
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

	// 加速验证：把探测间隔压到测试尺度。
	c.SetKeepAliveInterval(50 * time.Millisecond)
	c.StartKeepAlive()

	// 掐断连接（服务端可见地断开）——keepalive 探测应在数个周期内发现。
	c.KillConnectionForTesting()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !c.Alive() {
			return // 已判死
		}
		time.Sleep(50 * time.Millisecond)
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
