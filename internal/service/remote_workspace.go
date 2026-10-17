package service

// The SSH-workspace adapter: remote.Client satisfies the tools.RemoteFS
// contract, and OpenWorkspace turns a workspace string into a tools
// Workspace — local, or dialed-over-SSH when the target is ssh://.

import (
	"os"
	"path/filepath"

	"github.com/skeleton1231/zcode-my-go/internal/remote"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// sshClient adapts *remote.Client to tools.RemoteFS.
type sshClient struct{ c *remote.Client }

func (s sshClient) Exec(command string) (tools.ExecOutcome, error) {
	res, err := s.c.Exec(command)
	return tools.ExecOutcome{Output: res.Output, ExitCode: res.ExitCode}, err
}

func (s sshClient) ReadFile(name string) ([]byte, error) { return s.c.ReadFile(name) }

func (s sshClient) WriteFile(name string, data []byte, perm os.FileMode) error {
	return s.c.WriteFile(name, data, perm)
}

func (s sshClient) ReadDir(name string) ([]tools.DirEntry, error) {
	entries, err := s.c.ReadDir(name)
	out := make([]tools.DirEntry, len(entries))
	for i, e := range entries {
		out[i] = tools.DirEntry{Name: e.Name, IsDir: e.IsDir}
	}
	return out, err
}

func (s sshClient) Stat(name string) (bool, bool, error) { return s.c.Stat(name) }

func (s sshClient) ReadLink(name string) (string, bool, error) { return s.c.ReadLink(name) }

func (s sshClient) Glob(pattern string) ([]string, error) { return s.c.Glob(pattern) }

// openWorkspace builds the session's tools.Workspace: local paths stay
// local; ssh:// targets dial once (credentials from env: ZGO_SSH_KEY as
// a PEM, ZGO_SSH_PASSWORD) and delegate.
func (s *AgentService) openWorkspace(workspace string) (tools.Workspace, func(), error) {
	cleanup := func() {}
	if target := workspace; len(target) > 6 && target[:6] == "ssh://" {
		cfg, err := remote.ParseTarget(target)
		if err != nil {
			return tools.Workspace{}, cleanup, err
		}
		cfg.Key = os.Getenv("ZGO_SSH_KEY")
		cfg.Password = os.Getenv("ZGO_SSH_PASSWORD")
		client, err := remote.Dial(cfg)
		if err != nil {
			return tools.Workspace{}, cleanup, err
		}
		cleanup = func() { client.Close() }
		return tools.Workspace{Root: cfg.Root, Remote: sshClient{client}}, cleanup, nil
	}
	abs := workspace
	if !filepath.IsAbs(abs) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, abs)
		}
	}
	return tools.Workspace{Root: abs}, cleanup, nil
}
