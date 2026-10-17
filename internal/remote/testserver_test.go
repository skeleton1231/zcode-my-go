package remote

// An in-process SSH test server: a real x/crypto/ssh server on a
// loopback socket, serving "exec" sessions through the local shell and
// the "sftp" subsystem via pkg/sftp's default local-filesystem server
// rooted at a temp dir. The module's whole test bed — no protocol mocks.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// testServer is one running in-process SSH server.
type testServer struct {
	listener net.Listener
	address  string
	keyPEM   string // client private key (PEM) for Config.Key
	root     string // workspace root on disk
}

// startTestServer boots the server on an ephemeral loopback port.
func startTestServer(t *testing.T) *testServer {
	t.Helper()
	root := t.TempDir()

	// Host key.
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostDER, _ := x509.MarshalPKCS8PrivateKey(hostPriv)
	hostPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: hostDER})
	hostSigner, err := ssh.ParsePrivateKey(hostPEM)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{}
	cfg.AddHostKey(hostSigner)

	// Client key (returned as PEM for Config.Key).
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientDER, _ := x509.MarshalPKCS8PrivateKey(clientPriv)
	clientPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientDER}))
	clientSigner, err := ssh.ParsePrivateKey([]byte(clientPEM))
	if err != nil {
		t.Fatal(err)
	}
	cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
			return &ssh.Permissions{}, nil
		}
		return nil, fmt.Errorf("unknown key")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &testServer{listener: ln, address: ln.Addr().String(), keyPEM: clientPEM, root: root}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(conn, cfg, root)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return srv
}

// serveConn handles one SSH connection's channels.
func serveConn(conn net.Conn, cfg *ssh.ServerConfig, root string) {
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go serveSession(channel, requests, root)
	}
}

// serveSession answers exec and sftp-subsystem requests.
func serveSession(channel ssh.Channel, requests <-chan *ssh.Request, root string) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "exec":
			var payload struct{ Command string }
			ssh.Unmarshal(req.Payload, &payload)
			req.Reply(true, nil)
			line := stripCdPrefix(payload.Command)
			cmd := shellCommand(line)
			cmd.Dir = root
			out, runErr := cmd.CombinedOutput()
			channel.Write(out)
			code := 0
			if runErr != nil {
				code = exitCodeOf(runErr)
			}
			channel.SendRequest("exit-status", false,
				ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
			return
		case "subsystem":
			var payload struct{ Subsystem string }
			ssh.Unmarshal(req.Payload, &payload)
			if payload.Subsystem != "sftp" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			server, err := sftp.NewServer(channel)
			if err != nil {
				return
			}
			server.Serve()
			server.Close()
			return
		default:
			req.Reply(false, nil)
		}
	}
}

// shellCommand builds the platform shell invocation.
func shellCommand(line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", line)
	}
	return exec.Command("sh", "-c", line)
}

// exitCodeOf extracts an ExitError's code.
func exitCodeOf(err error) int {
	type exitError interface{ ExitCode() int }
	if ee, ok := err.(exitError); ok {
		return ee.ExitCode()
	}
	return 1
}

// stripCdPrefix drops the jail "cd '<root>' && " the client prepends —
// the server already runs in the root (test-bed only).
func stripCdPrefix(command string) string {
	if i := indexIn(command, " && "); i >= 0 && startsWithCd(command) {
		return command[i+4:]
	}
	return command
}

func startsWithCd(s string) bool { return len(s) >= 2 && s[0:2] == "cd" }

func indexIn(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
