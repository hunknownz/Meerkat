//go:build unix

package executor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ownGroup puts the child in a new process group so stops reach Pi and its children, and nothing else.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func termGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGTERM) }
func killGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }

func groupGone(pgid int) bool { return pgid > 0 && errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) }

func exitSignal(ps *os.ProcessState) string {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}

func bindGroup(cmd *exec.Cmd) error { return nil }
func releaseGroup(pid int)          {}
