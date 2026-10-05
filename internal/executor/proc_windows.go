//go:build windows

package executor

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var ownedJobs = struct {
	sync.Mutex
	m map[int]windows.Handle
}{m: map[int]windows.Handle{}}

func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
}

// Bind the suspended child before its first instruction, so every descendant
// inherits this job. A job handle is owned only by this live executor.
func bindGroup(cmd *exec.Cmd) error {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return e
	}
	good := false
	defer func() {
		if !good {
			windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if e != nil {
		return e
	}
	proc, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e != nil {
		return e
	}
	defer windows.CloseHandle(proc)
	if e = windows.AssignProcessToJobObject(job, proc); e != nil {
		return e
	}
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(snapshot)
	thread := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for e = windows.Thread32First(snapshot, &thread); e == nil; e = windows.Thread32Next(snapshot, &thread) {
		if thread.OwnerProcessID != uint32(cmd.Process.Pid) {
			continue
		}
		h, e := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, thread.ThreadID)
		if e != nil {
			return e
		}
		ownedJobs.Lock()
		ownedJobs.m[cmd.Process.Pid] = job
		ownedJobs.Unlock()
		_, e = windows.ResumeThread(h)
		windows.CloseHandle(h)
		if e != nil {
			ownedJobs.Lock()
			delete(ownedJobs.m, cmd.Process.Pid)
			ownedJobs.Unlock()
			return e
		}
		good = true
		return nil
	}
	return errors.New("suspended process thread unavailable")
}
func termGroup(pid int) { killGroup(pid) }
func killGroup(pid int) {
	ownedJobs.Lock()
	defer ownedJobs.Unlock()
	if job := ownedJobs.m[pid]; job != 0 {
		_ = windows.TerminateJobObject(job, 1)
	}
}
func groupGone(pid int) bool {
	ownedJobs.Lock()
	defer ownedJobs.Unlock()
	job := ownedJobs.m[pid]
	if job == 0 {
		return false
	}
	var info struct {
		Times                                 [4]int64
		PageFaults, Total, Active, Terminated uint32
	}
	e := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return e == nil && info.Active == 0
}
func releaseGroup(pid int) {
	ownedJobs.Lock()
	defer ownedJobs.Unlock()
	if job := ownedJobs.m[pid]; job != 0 {
		windows.CloseHandle(job)
		delete(ownedJobs.m, pid)
	}
}
func exitSignal(ps *os.ProcessState) string { return "" }
