package cli

import (
	"encoding/json"
	"testing"

	"github.com/hunknownz/Meerkat/internal/server"
)

func TestDispatchAndOperationCLIFlags(t *testing.T) {
	dir := privDir(t)
	ln, err := server.ListenUnix(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	requests := make(chan server.Request, 3)
	go func() {
		for i := 0; i < 3; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			req, err := server.DecodeRequest(conn)
			if err != nil {
				conn.Close()
				return
			}
			requests <- req
			json.NewEncoder(conn).Encode(server.Response{OK: true, Data: json.RawMessage(`{"accepted":true}`)})
			conn.Close()
		}
	}()
	for _, args := range [][]string{
		{"dispatch", "--data-dir", dir, "--task", "a", "--task", "b", "--request-id", "r", "--resume", "--acknowledge-interruption"},
		{"operation", "--data-dir", dir, "--operation", "o", "--wait-ms", "1000"},
		{"operation", "--data-dir", dir, "--request-id", "r"},
	} {
		if code, _, stderr := run(t, args...); code != ExitOK {
			t.Fatal(code, stderr)
		}
	}
	a, b, c := <-requests, <-requests, <-requests
	if a.Op != "dispatch" || a.RequestID != "r" || len(a.Tasks) != 2 || !a.Resume || !a.Acknowledge || b.Op != "wait-operation" || b.OperationID != "o" || b.WaitMillis != 1000 || c.Op != "operation" || c.RequestID != "r" {
		t.Fatal(a, b, c)
	}
	for _, args := range [][]string{{"dispatch", "--task", "a"}, {"operation"}, {"operation", "--operation", "o", "--request-id", "r"}, {"operation", "--request-id", "r", "--wait-ms", "1"}, {"operation", "--operation", "o", "--wait-ms", "30001"}} {
		if code, _, _ := run(t, args...); code != ExitUsage {
			t.Fatal(args, code)
		}
	}
}
