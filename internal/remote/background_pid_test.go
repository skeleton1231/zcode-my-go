package remote

import (
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestParsePidMarker 验证首行 pid 标记的解析契约（issue #166）：完整标
// 记 + 数字才认（CRLF 容忍），其他形态一律不是标记（原样回放）。
func TestParsePidMarker(t *testing.T) {
	if pid, ok := parsePidMarker([]byte("ZGO_BG_PID=4242")); !ok || pid != 4242 {
		t.Fatalf("plain marker = %d %v", pid, ok)
	}
	if pid, ok := parsePidMarker([]byte("ZGO_BG_PID=4242\r")); !ok || pid != 4242 {
		t.Fatalf("crlf marker = %d %v", pid, ok)
	}
	for _, bad := range []string{"ZGO_BG_PID=x", "ZGO_BG_PID=", "zgo_bg_pid=1", "junk line", "ZGO_BG_PID=-5"} {
		if _, ok := parsePidMarker([]byte(bad)); ok {
			t.Fatalf("%q wrongly parsed as marker", bad)
		}
	}
}

// TestWrapPosixBg 验证 POSIX 包装形态：pid 标记行在最前、exec 让命令继
// 承同一 pid（此后 kill 组长即杀全组）。
func TestWrapPosixBg(t *testing.T) {
	got := wrapPosixBg("/w root", "echo hi")
	want := "cd '/w root' && echo ZGO_BG_PID=$$ && exec echo hi"
	if got != want {
		t.Fatalf("wrap = %q, want %q", got, want)
	}
}

// TestConfirmKillLine 验证确证击杀命令的形态：进程组 + 组长 + 直系子进
// 程，全部静默容错（尽力而为，失败由调用方记日志）。
func TestConfirmKillLine(t *testing.T) {
	got := confirmKillLine(99)
	for _, want := range []string{"kill -TERM -- -99", "kill -TERM 99", "pkill -TERM -P 99"} {
		if !strings.Contains(got, want) {
			t.Fatalf("confirm line %q missing %q", got, want)
		}
	}
}

// TestBgReaderSwallow 验证输出读者：标记行吞掉（模型不见 pid 噪音）、
// pid 入册、余量字节不丢。
func TestBgReaderSwallow(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &BgProcess{pr: pr, posix: true}
	go func() {
		pw.WriteString("ZGO_BG_PID=4242\n") // 标记行
		pw.WriteString("real ")             // 跨读回放
		pw.WriteString("output\n")
		pw.Close()
	}()
	out, err := io.ReadAll(&bgReader{p: p})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "real output\n" {
		t.Fatalf("output = %q, marker leaked or bytes lost", out)
	}
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}
}

// TestBgReaderNoMarkerReplaysByteExact: 首行不是标记（cmd 远端不包装、
// 或远端 shell 未按预期回显）时输出原样。
func TestBgReaderNoMarkerReplaysByteExact(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &BgProcess{pr: pr, posix: true}
	go func() {
		pw.WriteString("zgo_$$\nraw output\n") // cmd 形态的原样回显
		pw.Close()
	}()
	out, err := io.ReadAll(&bgReader{p: p})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "zgo_$$\nraw output\n" {
		t.Fatalf("output = %q, want byte-exact replay", out)
	}
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid != 0 {
		t.Fatalf("pid = %d, want 0 for non-marker", pid)
	}
}

// TestPosixProbeAgainstTestServer: 探测与平台一致——cmd 服务器（Windows）
// 原样回显 $$ 判非 POSIX；sh 服务器（POSIX）判 POSIX。端到端确保探测不
// 误伤现有后台路径。
func TestPosixProbeAgainstTestServer(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("probe branch asserted on Windows (cmd server); POSIX branch covered by suite on POSIX runners")
	}
	c := dialTestServer(t)
	if c.posixShell() {
		t.Fatal("cmd server must probe as non-POSIX ($$ stays literal)")
	}
}
