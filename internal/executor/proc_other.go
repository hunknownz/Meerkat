//go:build !unix

package executor

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
)

// Process-group ownership is only implemented on Unix; elsewhere Execute refuses to start.
func ownGroup(cmd *exec.Cmd)                {}
func termGroup(pgid int)                    {}
func killGroup(pgid int)                    {}
func groupGone(int) bool                    { return false }
func exitSignal(ps *os.ProcessState) string { return "" }
func ownedByMe(fi fs.FileInfo) bool         { return false }
func openNoFollow(string) (*os.File, error) { return nil, errors.New("unsupported platform") }
