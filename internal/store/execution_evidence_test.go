package store

import (
	"encoding/json"
	"strings"
	"testing"

	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

func TestExecutionEvidenceReservationsUnknownAndNoPrivateHistory(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 3)
	r := budgetRequest(1, 100, 500)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	ss, b, err := s.ExecutionEvidence(p.TaskID)
	if err != nil || len(ss) != 1 || b.AvailableTokens == nil || *b.AvailableTokens != 400 || b.ReservedTokens != 600 || b.PendingRequests != 1 {
		t.Fatal(ss, b, err)
	}
	settleRequest(t, s, l.Token, p.RunID, budget.Settlement{ID: r.ID, State: budget.Unknown})
	if err := s.ReconcileRequestBudgetsOwned(l.Token); err != nil {
		t.Fatal(err)
	}
	ss, b, err = s.ExecutionEvidence(p.TaskID)
	if err != nil || b.AvailableTokens != nil || b.ReservedTokens != 600 || b.UnknownRequests != 1 || b.ConfirmedTokens != 0 {
		t.Fatal(ss, b, err)
	}
	raw, _ := json.Marshal(ss)
	for _, private := range []string{"fileRef", "fileDigest", "providerId", "history.jsonl", "profileDigest", "contractDigest"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private session evidence exposed", private)
		}
	}
}

func TestExecutionEvidenceOverrunPreservesConfirmedUsage(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 3)
	r := budgetRequest(1, 100, 100)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(r.ID, 200, 50))
	_, b, err := s.ExecutionEvidence(p.TaskID)
	if err != nil || !b.Overrun || b.AvailableTokens == nil || *b.AvailableTokens != 0 || b.ConfirmedTokens != 250 || b.ReservedTokens != 0 {
		t.Fatal(b, err)
	}
	left, _, err := s.RequestHeadroomOwned(l.Token, p.RunID)
	if err != nil || left != 0 {
		t.Fatal("overrun became uncertain", left, err)
	}
}
