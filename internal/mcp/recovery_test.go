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

func TestRecoveryToolsAuthorizationInspectionAndLostReply(t *testing.T) {
	p := model.RecoveryProposal{SchemaVersion: 1, TaskID: taskA, RunID: runA, CompletionDigest: strings.Repeat("a", 64), EvidenceDigest: strings.Repeat("b", 64)}
	in := model.RecoveryInput{Proposal: p, RequestID: stopA, AuthorizationRef: "PRIVATE_RECOVERY_PERMISSION", Apply: true}
	calls := 0
	s := controlled(snapshotWith(), func(_ context.Context, r server.Request) (server.Response, error) {
		calls++
		var v any
		switch r.Op {
		case "inspect-recovery":
			v = model.RecoveryInspection{SchemaVersion: 1, TaskID: taskA, RunID: runA, Status: "verified", Checks: []model.RecoveryCheck{}, Proposal: &p}
		case "apply-recovery", "recovery-decision":
			v = model.RecoveryReceipt{RequestID: stopA, TaskID: taskA, RunID: runA, CreatedAt: "2026-10-03T00:00:00Z"}
		default:
			t.Fatal(r.Op)
		}
		raw, _ := json.Marshal(v)
		return server.Response{OK: true, Data: raw}, nil
	})
	for _, args := range []string{`{}`, `{"proposal":{},"apply":true}`, `{"proposal":{},"requestId":"` + stopA + `","authorizationRef":"test","apply":false}`} {
		if errCode(callControl(t, context.Background(), s, ToolApplyRecovery, args)) != codeInvalidParams {
			t.Fatal("implicit recovery accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid recovery reached daemon")
	}
	d := data(t, callControl(t, context.Background(), s, ToolInspectRecovery, `{"taskId":"`+taskA+`"}`))
	if d["inspection"].(map[string]any)["status"] != "verified" {
		t.Fatal(d)
	}
	b, _ := json.Marshal(in)
	reply := callControl(t, context.Background(), s, ToolApplyRecovery, string(b))
	raw, _ := json.Marshal(reply)
	if strings.Contains(string(raw), in.AuthorizationRef) || strings.Contains(string(raw), p.EvidenceDigest) {
		t.Fatal("private recovery evidence leaked")
	}
	data(t, callControl(t, context.Background(), s, ToolGetRecovery, `{"requestId":"`+stopA+`"}`))
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		calls++
		return server.Response{}, errors.New("PRIVATE_TRANSPORT_ERROR")
	}
	before := calls
	d = data(t, callControl(t, context.Background(), s, ToolApplyRecovery, string(b)))
	if d["status"] != "unknown" || calls != before+1 {
		t.Fatal("lost reply automatically retried", d, calls)
	}
}
