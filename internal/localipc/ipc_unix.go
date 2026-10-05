//go:build unix

// Package localipc exposes private local transports with platform ownership.
package localipc

import (
	"context"
	"errors"
	"github.com/hunknownz/Meerkat/internal/platform"
	"net"
	"os"
	"syscall"
	"time"
)

func Endpoint(path string) string { return path }
func Check(path string) error {
	fi, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if fi.Mode()&os.ModeSocket == 0 || !platform.Private(path, fi, 0o600) {
		return errors.New("unsafe local endpoint")
	}
	return nil
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if e := Check(path); e != nil {
		return nil, e
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
func Listen(path string) (net.Listener, error) {
	if _, e := os.Lstat(path); e == nil {
		if e = Check(path); e != nil {
			return nil, e
		}
		c, e := net.DialTimeout("unix", path, time.Second)
		if e == nil {
			c.Close()
			return nil, errors.New("local endpoint active")
		}
		if !errors.Is(e, syscall.ECONNREFUSED) {
			return nil, e
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	ln, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(path, 0o600); e != nil {
		ln.Close()
		return nil, e
	}
	return ln, nil
}
