//go:build !windows

package remotetest

import "os/exec"

// 非 Windows 无 Job Object：treeJob 退化为 nil（调用方走进程击杀）。
// 测试命令在 sh -c 下多为直接 exec（无孙进程），单点击杀足够。
func attachTreeJob(*exec.Cmd) (*treeJob, error) { return nil, nil }

type treeJob struct{}

func (*treeJob) kill() {}
