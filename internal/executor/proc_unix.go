//go:build unix

package executor

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"
)

// ownGroup puts the child in a new process group so stops reach Pi and its children, and nothing else.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func termGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGTERM) }
func killGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }

func exitSignal(ps *os.ProcessState) string {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}

func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func ownedByMe(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
