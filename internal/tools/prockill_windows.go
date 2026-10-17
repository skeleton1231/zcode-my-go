//go:build windows

package tools

import (
	"os/exec"
	"strconv"
)

// setupTreeKill Windows 上无需启动前设置：taskkill 按父 PID 递归整棵树。
func setupTreeKill(*exec.Cmd) {}

// killTree 击杀 pid 及其全部后代（issue #7）：taskkill /T 按进程树递归，
// /F 强制。exec 默认的 Process.Kill 只杀直接子进程（powershell.exe），
// Start-Process 派生的孙进程会存活并继续运行。
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Cancel 回调运行时原 ctx 已取消：taskkill 用独立命令，不受影响。
	return exec.Command("taskkill", "/T", "/F",
		"/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
