//go:build unix

// Package platform supplies ownership, private files and process observation.
// Policy stays in the caller; an uncertain observation never becomes absence.
package platform

import (
	"errors"
	"os"
	"syscall"
)

func Owned(path string, fi os.FileInfo) bool {
	if fi == nil || fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
func Private(path string, fi os.FileInfo, perm os.FileMode) bool {
	return Owned(path, fi) && fi.Mode().Perm() == perm
}
func Mkdir(path string, perm os.FileMode) error { return os.Mkdir(path, perm) }
func Chmod(path string, perm os.FileMode) error { return os.Chmod(path, perm) }
func OpenFile(path string, flags int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
}
func SyncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	e := syscall.Kill(pid, 0)
	return e == nil || !errors.Is(e, syscall.ESRCH)
}
func GroupAbsent(pid, group int) bool {
	return pid > 0 && group > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
}
