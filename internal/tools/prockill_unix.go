//go:build !windows

package tools

import (
	"errors"
	"os/exec"
	"syscall"
)

// setupTreeKill 把子进程设为独立进程组组长，让负 PID 组杀覆盖全部后代。
func setupTreeKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree 击杀整条进程组（issue #7）：负 PID 表示 Setpgid 建的整个组，
// 覆盖子进程派生的孙进程；ESRCH 表示组已退出，视作成功。
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil &&
		!errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
