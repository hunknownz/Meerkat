package cli

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func TestControlCLIExplicitApplyAndReadOnlyReceipt(t *testing.T) {
	d := privDir(t)
	ln, e := server.ListenUnix(d)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	id := "88000000-0000-4000-8000-000000000001"
	task := "99000000-0000-4000-8000-000000000001"
	got := make(chan server.Request, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			func(c net.Conn) {
				defer c.Close()
				var r server.Request
				if json.NewDecoder(c).Decode(&r) != nil {
					return
				}
				got <- r
				v := model.ControlReceipt{RequestID: id, TaskID: task, RunID: id, SessionID: &task, Kind: "wrap_up", State: model.ControlAccepted, CreatedAt: "2026-10-03T00:00:00Z", UpdatedAt: "2026-10-03T00:00:00Z", RunState: model.RunRunning}
				b, _ := json.Marshal(v)
				json.NewEncoder(c).Encode(server.Response{OK: true, Data: b})
			}(c)
		}
	}()
	args := []string{"control", "wrap-up", "--data-dir", d, "--run", id, "--session", task, "--request-id", id, "--authorization", "PRIVATE_TEST_AUTH"}
	if code, _, _ := run(t, args...); code != ExitUsage {
		t.Fatal("implicit send", code)
	}
	select {
	case <-got:
		t.Fatal("missing --apply reached daemon")
	default:
	}
	if code, out, e := run(t, append(args, "--apply")...); code != 0 || strings.Contains(out, "PRIVATE_TEST_AUTH") {
		t.Fatal(code, e, out)
	}
	r := <-got
	var in model.WrapUpInput
	json.Unmarshal(r.Input, &in)
	if r.Op != "request-wrap-up" || !model.ValidWrapUpInput(in) || in.AuthorizationRef != "PRIVATE_TEST_AUTH" || in.SessionID != task {
		t.Fatal("input changed")
	}
	if code, _, e := run(t, "control", "receipt", "--data-dir", d, "--request-id", id); code != 0 {
		t.Fatal(code, e)
	}
	if r := <-got; r.Op != "control-receipt" || r.RequestID != id {
		t.Fatal("query resubmitted control", r.Op)
	}
	ln.Close()
	<-done
}

func TestFollowUpCLIReadsBoundedPlainTextFromFileAndStdin(t *testing.T) {
	d := privDir(t)
	ln, err := server.ListenUnix(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan server.Request, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req server.Request
			if json.NewDecoder(c).Decode(&req) == nil {
				got <- req
				session := "99000000-0000-4000-8000-000000000001"
				receipt := model.ControlReceipt{RequestID: "88000000-0000-4000-8000-000000000001", TaskID: session, RunID: "88000000-0000-4000-8000-000000000001", SessionID: &session, Kind: "follow_up", State: model.ControlAccepted, CreatedAt: "2026-10-04T00:00:00Z", UpdatedAt: "2026-10-04T00:00:00Z", RunState: model.RunRunning}
				body, _ := json.Marshal(receipt)
				json.NewEncoder(c).Encode(server.Response{OK: true, Data: body})
			}
			c.Close()
		}
	}()
	text := "追加说明：保留检查点。\nKeep the declared scope."
	file := filepath.Join(d, "direction.txt")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{file, "-"} {
		var out, errOut syncBuf
		args := []string{"control", "follow-up", "--data-dir", d, "--run", "88000000-0000-4000-8000-000000000001", "--session", "99000000-0000-4000-8000-000000000001", "--request-id", "88000000-0000-4000-8000-000000000001", "--authorization", "PRIVATE_TEST_AUTH", "--apply", "--input", input}
		code := Run(Env{Ctx: context.Background(), Stdin: strings.NewReader(text), Stdout: &out, Stderr: &errOut}, args)
		if code != 0 {
			t.Fatal(code, errOut.String())
		}
		req := <-got
		var v model.WrapUpInput
		if json.Unmarshal(req.Input, &v) != nil || req.Op != "request-follow-up" || v.Message != text || !model.ValidFollowUpInput(v) {
			t.Fatal("plain text changed")
		}
		if strings.Contains(out.String(), text) || strings.Contains(out.String(), "PRIVATE_TEST_AUTH") {
			t.Fatal("private instruction exposed")
		}
	}
	if _, err := readInput(Env{Stdin: strings.NewReader(text)}, "-"); err == nil {
		t.Fatal("JSON command accepted plain text")
	}
	if _, err := readInputBytes(Env{Stdin: strings.NewReader(strings.Repeat("a", 16001))}, "-", 16000); err == nil {
		t.Fatal("unbounded instruction read")
	}
}
