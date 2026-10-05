//go:build !unix && !windows

package executor

import (
	"errors"
	"os"
	"os/exec"
)

// Process-group ownership is only implemented on Unix; elsewhere Execute refuses to start.
func ownGroup(cmd *exec.Cmd)                {}
func termGroup(pgid int)                    {}
func killGroup(pgid int)                    {}
func groupGone(int) bool                    { return false }
func exitSignal(ps *os.ProcessState) string { return "" }
func bindGroup(cmd *exec.Cmd) error         { return errors.New("unsupported platform") }
func releaseGroup(pid int)                  {}
