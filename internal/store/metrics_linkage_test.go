package store

import (
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
	"strings"
	"testing"
)

func TestMetricsLinksSessionAndLedgerWithoutInventingUsage(t *testing.T) {
	s, l, ss, p := budgetFixture(t, 1000, 10)
	r := budgetRequest(1, 100, 100)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRequestOwned(l.Token, p.RunID, budget.Begin{ID: r.ID, Digest: strings.Repeat("e", 64)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateLimitRetryOwned(l.Token, p.RunID, budget.RateLimitReport{ID: r.ID, Evidence: budget.RateLimitEvidence{Status: 429, Proof: "rejected-before-generation"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.MetricsRows()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	got := rows[0]
	if got.SessionID == nil || *got.SessionID != ss.ID || got.RequestCount == nil || *got.RequestCount != 1 || got.UnknownRequests == nil || *got.UnknownRequests != 0 || got.RateLimitedRequests == nil || *got.RateLimitedRequests != 1 {
		t.Fatal(got)
	}
	if got.Total != nil || got.CostUsd != nil || got.ModelSeconds != nil || got.FirstReviewPass != nil {
		t.Fatal("invented measurement", got)
	}
}
