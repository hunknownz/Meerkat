//go:build windows

package localipc

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestNativePipeOwnershipAndHalfClose(t *testing.T) {
	path := Endpoint(filepath.Join(t.TempDir(), "commands"))
	ln, e := Listen(path)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	if other, e := Listen(path); e == nil {
		other.Close()
		t.Fatal("replaced active pipe")
	}
	done := make(chan error, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		b, e := io.ReadAll(c)
		if e == nil {
			_, e = c.Write(b)
		}
		done <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, e := Dial(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Write([]byte("request"))
	cw, ok := c.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("half close unavailable")
	}
	if e = cw.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(c)
	if e != nil || string(b) != "request" {
		t.Fatalf("reply %q %v", b, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
