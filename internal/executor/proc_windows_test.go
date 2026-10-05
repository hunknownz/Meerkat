//go:build windows

package executor

import (
	"github.com/hunknownz/Meerkat/internal/platform"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestNativeProcessHelper(t *testing.T) {
	mode := os.Getenv("MEERKAT_JOB_HELPER")
	if mode == "" {
		return
	}
	if mode == "parent" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestNativeProcessHelper$")
		cmd.Env = append(os.Environ(), "MEERKAT_JOB_HELPER=leaf")
		if e := cmd.Start(); e != nil {
			os.Exit(3)
		}
		os.WriteFile(os.Getenv("MEERKAT_JOB_PID"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}
	time.Sleep(60 * time.Second)
	os.Exit(0)
}
func TestNativeOwnedJobStopsDescendants(t *testing.T) {
	exe, _ := os.Executable()
	file := filepath.Join(t.TempDir(), "leaf.pid")
	cmd := exec.Command(exe, "-test.run=^TestNativeProcessHelper$")
	cmd.Env = append(os.Environ(), "MEERKAT_JOB_HELPER=parent", "MEERKAT_JOB_PID="+file)
	ownGroup(cmd)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Wait()
	defer releaseGroup(cmd.Process.Pid)
	if e := bindGroup(cmd); e != nil {
		cmd.Process.Kill()
		t.Fatal(e)
	}
	defer killGroup(cmd.Process.Pid)
	deadline := time.Now().Add(10 * time.Second)
	var leaf int
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(file)
		leaf, _ = strconv.Atoi(string(b))
		if leaf > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if leaf <= 0 || !platform.Alive(leaf) {
		t.Fatal("descendant did not start")
	}
	if groupGone(cmd.Process.Pid) {
		t.Fatal("live job reported absent")
	}
	killGroup(cmd.Process.Pid)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !groupGone(cmd.Process.Pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if !groupGone(cmd.Process.Pid) || platform.Alive(leaf) {
		t.Fatal("owned descendant survived stop")
	}
	if groupGone(987654321) {
		t.Fatal("unknown process treated as owned")
	}
}
