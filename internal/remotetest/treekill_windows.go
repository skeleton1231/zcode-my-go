//go:build windows

package remotetest

// 断开即杀的确定性实现：命令进程进 kill-on-close 的 Job Object，关句
// 柄时整棵树（cmd.exe 的孙进程——ping 等）同步死亡。此前靠 taskkill /T
// 的异步宽限，cmd.Wait 只等直接子进程，孙进程死亡时序在全量测试负载
// 下真实竞态过（临时目录清理撞文件占用）。

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobExtendedLimit 是 JOBOBJECT_EXTENDED_LIMIT_INFORMATION（x/sys 只提
// 供 BASIC 版本；字段布局按 winnt.h）。
type jobExtendedLimit struct {
	BasicLimitInformation windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
	IoInfo                windows.IO_COUNTERS
	ProcessMemoryLimit    uint32
	JobMemoryLimit        uint32
	PeakProcessMemoryUsed uint32
	PeakJobMemoryUsed     uint32
}

// treeJob 持有 Job Object 句柄；kill 关句柄即杀整树。
type treeJob struct{ handle windows.Handle }

// attachTreeJob 把已启动的命令进程放进 kill-on-close 的 Job Object。
func attachTreeJob(cmd *exec.Cmd) (*treeJob, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info jobExtendedLimit
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// os.Process 不暴露句柄：按 pid 自开（赋 Job 只需 SET_QUOTA）。
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA, false,
		uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign job: %w", err)
	}
	return &treeJob{handle: job}, nil
}

// kill 关闭 Job 句柄——kill-on-close 使整棵树同步终止。
func (j *treeJob) kill() { windows.CloseHandle(j.handle) }
