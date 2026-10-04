package store

import (
	"errors"
	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRateLimitRequiresDefiniteRejectionAndBoundsNewAttempts(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 10)
	for i := 1; i <= 4; i++ {
		r := budgetRequest(i, 100, 100)
		if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
			t.Fatal(err)
		}
		if err := s.BeginRequestOwned(l.Token, p.RunID, budget.Begin{ID: r.ID, Digest: strings.Repeat("e", 64)}); err != nil {
			t.Fatal(err)
		}
		report := budget.RateLimitReport{ID: r.ID, Evidence: budget.RateLimitEvidence{Status: 429, Proof: "rejected-before-generation"}}
		bad := report
		bad.Evidence.Proof = "HTTP 429"
		if _, err := s.RateLimitRetryOwned(l.Token, p.RunID, bad); err == nil {
			t.Fatal("unproven rejection accepted")
		}
		grant, err := s.RateLimitRetryOwned(l.Token, p.RunID, report)
		if err != nil {
			t.Fatal(err)
		}
		if grant.Retry != (i <= 3) {
			t.Fatal("retry bound", i, grant)
		}
		if i <= 3 && grant.WaitMillis != int64(1000<<(i-1)) {
			t.Fatal(grant)
		}
		again, err := s.RateLimitRetryOwned(l.Token, p.RunID, report)
		if err != nil || again != grant {
			t.Fatal("rejection dedup", again, err)
		}
		if err := s.BeginRequestOwned(l.Token, p.RunID, budget.Begin{ID: r.ID, Digest: strings.Repeat("e", 64)}); err == nil {
			t.Fatal("old permit replayed")
		}
	}
	records, err := s.RequestBudgetRecords(p.RunID)
	if err != nil || len(records) != 4 {
		t.Fatal(records, err)
	}
	for _, r := range records {
		if r.State != budget.RateLimited || r.Settlement.Tokens.Total != nil {
			t.Fatal("rejection counted as measured zero", r)
		}
	}
	// Later attempts cannot change the decision retained for the first request.
	first := budget.RateLimitReport{ID: records[0].Request.ID, Evidence: *records[0].Settlement.Rejection}
	retained, err := s.RateLimitRetryOwned(l.Token, p.RunID, first)
	if err != nil || !retained.Retry || retained.WaitMillis != 1000 {
		t.Fatal(retained, err)
	}
	_, ev, err := s.ExecutionEvidence(p.TaskID)
	if err != nil || ev.UnknownRequests != 0 || ev.PendingRequests != 0 || ev.ConfirmedTokens != 0 {
		t.Fatal(ev, err)
	}
	out, err := s.CloseRequestBudgetOwned(l.Token, p.RunID)
	if err != nil || !out.Confirmed {
		t.Fatal(out, err)
	}
	// No successful generation has reported usage. Total remains unknown.
	if out.Usage.Tokens.Total != nil {
		t.Fatal("invented actual use", out.Usage)
	}
}

func TestRateLimitDecisionBackupPreservesUnknownUsageAndRejectsCorruption(t *testing.T) {
	s, l, ss, p := budgetFixture(t, 1000, 10)
	r := budgetRequest(1, 100, 100)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRequestOwned(l.Token, p.RunID, budget.Begin{ID: r.ID, Digest: strings.Repeat("e", 64)}); err != nil {
		t.Fatal(err)
	}
	grant, err := s.RateLimitRetryOwned(l.Token, p.RunID, budget.RateLimitReport{ID: r.ID, Evidence: budget.RateLimitEvidence{Status: 429, Proof: "rejected-before-generation"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CloseRequestBudgetOwned(l.Token, p.RunID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishSessionRunOwned(l.Token, ss, p.RunID, func(st *model.State) error {
		st.Runs[0].State, st.Tasks[0].State = model.RunStopped, model.TaskStopped
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "rate-limit.db")
	if err = s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err = Restore(backup, dst); err != nil {
		t.Fatal(err)
	}
	got, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	rows, err := got.RequestBudgetRecords(p.RunID)
	if err != nil || len(rows) != 1 || rows[0].Retry == nil || *rows[0].Retry != grant || rows[0].Settlement.Tokens.Total != nil {
		t.Fatal(rows, err)
	}
	rows[0].Retry.WaitMillis = 30001
	editBackup(t, backup, "UPDATE budget_requests SET payload=? WHERE id=?", mustJSON(rows[0]), r.ID)
	if !errors.Is(ValidateBackup(backup), ErrBadBackup) {
		t.Fatal("invalid retry grant restored")
	}
}

func TestRateLimitDeadlineRefusesWaitAndRetainsDecision(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 10)
	p.Deadline = time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec("UPDATE budget_runs SET payload=? WHERE run_id=?", mustJSON(p), p.RunID); err != nil {
		t.Fatal(err)
	}
	r := budgetRequest(1, 100, 100)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRequestOwned(l.Token, p.RunID, budget.Begin{ID: r.ID, Digest: strings.Repeat("e", 64)}); err != nil {
		t.Fatal(err)
	}
	v := budget.RateLimitReport{ID: r.ID, Evidence: budget.RateLimitEvidence{Status: 429, Proof: "rejected-before-generation", RetryAfterMillis: 30000}}
	grant, err := s.RateLimitRetryOwned(l.Token, p.RunID, v)
	if err != nil || grant.Retry || grant.WaitMillis != 0 {
		t.Fatal(grant, err)
	}
	v.Evidence.RetryAfterMillis = 1
	if _, err := s.RateLimitRetryOwned(l.Token, p.RunID, v); err == nil {
		t.Fatal("changed rejection rewrote original decision")
	}
}
