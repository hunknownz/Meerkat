package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func TestRunControlsStrictAuthorizationReceiptAndLostReply(t *testing.T) {
	in := model.WrapUpInput{RunID: runA, SessionID: runB, RequestID: stopA, AuthorizationRef: "PRIVATE_CONTROL_PERMISSION", Apply: true}
	calls := 0
	s := controlled(snapshotWith(), func(_ context.Context, r server.Request) (server.Response, error) {
		calls++
		if r.Op != "request-wrap-up" && r.Op != "control-receipt" {
			t.Fatal(r.Op)
		}
		v := model.ControlReceipt{RequestID: stopA, TaskID: taskA, RunID: runA, SessionID: &in.SessionID, Kind: "wrap_up", State: model.ControlAcknowledged, Disposition: ptr("queued"), CreatedAt: "2026-10-03T00:00:00Z", UpdatedAt: "2026-10-03T00:00:00Z", RunState: model.RunRunning}
		b, _ := json.Marshal(v)
		return server.Response{OK: true, Data: b}, nil
	})
	for _, args := range []string{`{}`, `{"runId":"` + runA + `","sessionId":"` + runB + `","requestId":"` + stopA + `","authorizationRef":"test","apply":false}`, `{"runId":"` + runA + `","runId":"` + runB + `","apply":true}`} {
		if errCode(callControl(t, context.Background(), s, ToolRequestWrapUp, args)) != codeInvalidParams {
			t.Fatal("implicit control accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached daemon")
	}
	b, _ := json.Marshal(in)
	reply := callControl(t, context.Background(), s, ToolRequestWrapUp, string(b))
	d := data(t, reply)["receipt"].(map[string]any)
	if d["state"] != "acknowledged" || d["outcome"] != nil || d["runState"] != model.RunRunning {
		t.Fatal(d)
	}
	raw, _ := json.Marshal(reply)
	if strings.Contains(string(raw), in.AuthorizationRef) {
		t.Fatal("approval leaked")
	}
	data(t, callControl(t, context.Background(), s, ToolGetControlReceipt, `{"requestId":"`+stopA+`"}`))
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		calls++
		return server.Response{}, errors.New("PRIVATE_TRANSPORT_ERROR")
	}
	before := calls
	d = data(t, callControl(t, context.Background(), s, ToolRequestWrapUp, string(b)))
	if d["status"] != "unknown" || calls != before+1 {
		t.Fatal("lost reply retried", d, calls)
	}
}
