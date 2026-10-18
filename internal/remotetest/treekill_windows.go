//go:build windows

package remotetest

// 断开即杀的确定性实现：命令进程进 kill-on-close 的 Job Object，关句
// 柄时整棵树（cmd.exe 的孙进程——ping 等）同步死亡。此前靠 taskkill /T
// 的异步宽限，cmd.Wait 只等直接子进程，孙进程死亡时序在全量测试负载
// 下真实竞态过（临时目录清理撞文件占用）。

import (
	"fmt"
	"os/exec"
	"time"
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

// kill 显式终止整棵 Job 进程树，等成员清空（终止进程的句柄释放是异
// 步的——TempDir 清理撞文件占用的竞态在这里关死）再关句柄。
func (j *treeJob) kill() {
	_ = windows.TerminateJobObject(j.handle, 1)
	j.waitEmpty()
	windows.CloseHandle(j.handle)
}

// jobProcessList 查询 Job 当前成员 PID（JOBOBJECT_BASIC_PROCESS_ID_LIST）。
func jobProcessList(job windows.Handle) ([]uint32, error) {
	const maxPIDs = 64
	size := uint32(8 + 8 + maxPIDs*4)
	buf := make([]byte, size)
	var ret uint32
	if err := windows.QueryInformationJobObject(job,
		windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&buf[0])), size, &ret); err != nil {
		return nil, err
	}
	count := *(*uint32)(unsafe.Pointer(&buf[4])) // 第二个字段：在列 PID 数
	pids := make([]uint32, 0, count)
	for i := uint32(0); i < count && i < maxPIDs; i++ {
		pid := *(*uint32)(unsafe.Pointer(&buf[8+i*4]))
		if pid != 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// waitEmpty 轮询直到 Job 成员清空（终止进程的句柄释放是异步的——
// TempDir 清理撞文件占用的最后一格竞态在这里关死）。
func (j *treeJob) waitEmpty() {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pids, err := jobProcessList(j.handle)
		if err == nil && len(pids) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
