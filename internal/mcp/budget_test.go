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

func budgetProposalFixture() model.BudgetProposal {
	return model.BudgetProposal{BudgetIncrease: model.BudgetIncrease{TaskID: taskA, AddTokens: 100, Reason: "Finish original scope"}, SchemaVersion: 1, Revision: 0, CurrentTokens: 1000, CurrentWallSeconds: 600, ContractDigest: strings.Repeat("a", 64), EvidenceDigest: strings.Repeat("b", 64)}
}
func TestBudgetToolsValidateAuthorizeAndRecoverWithoutRetry(t *testing.T) {
	p := budgetProposalFixture()
	in := model.BudgetDecisionInput{Proposal: p, RequestID: stopA, AuthorizationRef: "PRIVATE_EXPLICIT_TEST_AUTH", Apply: true}
	calls := 0
	s := controlled(snapshotWith(), func(_ context.Context, r server.Request) (server.Response, error) {
		calls++
		var v any
		switch r.Op {
		case "propose-budget":
			v = p
		case "apply-budget-decision", "budget-decision":
			v = model.BudgetDecisionSummary{RequestID: stopA, Revision: 1, AddTokens: 100, Reason: p.Reason, CreatedAt: "2026-10-03T00:00:00Z"}
		default:
			t.Fatal(r.Op)
		}
		b, _ := json.Marshal(v)
		return server.Response{OK: true, Data: b}, nil
	})
	for _, args := range []string{`{}`, `{"proposal":{},"apply":true}`, `{"requestId":"` + stopA + `","apply":false}`, `{"proposal":{},"requestId":"` + stopA + `","authorizationRef":"test","apply":null}`} {
		if e := errCode(callControl(t, context.Background(), s, ToolApplyBudget, args)); e != codeInvalidParams {
			t.Fatal(e)
		}
	}
	if calls != 0 {
		t.Fatal("invalid grant reached authority")
	}
	b, _ := json.Marshal(p.BudgetIncrease)
	r := data(t, callControl(t, context.Background(), s, ToolProposeBudget, string(b)))
	if r["proposal"] == nil {
		t.Fatal(r)
	}
	b, _ = json.Marshal(in)
	reply := callControl(t, context.Background(), s, ToolApplyBudget, string(b))
	raw, _ := json.Marshal(reply)
	if strings.Contains(string(raw), in.AuthorizationRef) || strings.Contains(string(raw), p.EvidenceDigest) {
		t.Fatal("authority leaked in receipt")
	}
	if d := data(t, reply)["receipt"].(map[string]any); d["revision"] != float64(1) {
		t.Fatal(d)
	}
	data(t, callControl(t, context.Background(), s, ToolGetBudgetDecision, `{"requestId":"`+stopA+`"}`))
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		calls++
		// A readable response with the wrong amount is not a matching receipt.
		v := model.BudgetDecisionSummary{RequestID: stopA, Revision: 1, AddTokens: 101, Reason: p.Reason, CreatedAt: "2026-10-03T00:00:00Z"}
		b, _ := json.Marshal(v)
		return server.Response{OK: true, Data: b}, nil
	}
	if result(t, callControl(t, context.Background(), s, ToolApplyBudget, string(b)))["isError"] != true {
		t.Fatal("mismatched allowance receipt accepted")
	}
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		calls++
		return server.Response{}, errors.New("lost reply with PRIVATE_PROVIDER_KEY")
	}
	before := calls
	r = data(t, callControl(t, context.Background(), s, ToolApplyBudget, string(b)))
	if r["status"] != "unknown" || calls != before+1 {
		t.Fatal("lost grant retried", r, calls)
	}
}
