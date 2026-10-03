package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

func budgetFixture(t *testing.T, tokens, requests int64) (*Store, model.ControllerLease, model.Session, budget.Policy) {
	t.Helper()
	s, l, ss, rid := sessionFixture(t)
	var policy budget.Policy
	if err := s.UpdateOwned(l.Token, func(st *model.State) error {
		st.Runs = nil // A legacy run with unknown usage must not silently spend zero.
		p := model.Profile{ID: "profile", ProjectID: "p", Role: "developer", Executor: "fixture", Provider: "fixture", Model: "text-model"}
		st.Profiles = []model.Profile{p}
		tt := &st.Tasks[0]
		tt.ProfileIDs = map[string]string{"developer": p.ID}
		b := model.DefaultBudget()
		b.MaxTokens = tokens
		tt.Budget = &b
		ss.ProfileDigest, ss.ContractDigest = model.FrozenProfileDigest(p), model.FrozenTaskDigest(st, *tt)
		policy = budget.Policy{RunID: rid, TaskID: ss.TaskID, SessionID: ss.ID, ProfileID: p.ID, ProfileDigest: ss.ProfileDigest, ContractDigest: ss.ContractDigest,
			Provider: p.Provider, Model: p.Model, Version: budget.PolicyVersion, Deadline: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), TaskTokens: tokens, RunTokens: tokens, TaskRequests: requests, State: "open"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.StartSessionRunOwned(l.Token, ss, rid, true, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: rid, TaskID: ss.TaskID, ProfileID: ss.ProfileID, Role: "developer", State: model.RunRunning, UpdatedAt: now()})
		st.Tasks[0].State = model.TaskImplementing
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenRequestBudgetOwned(l.Token, policy); err != nil {
		t.Fatal(err)
	}
	return s, l, ss, policy
}

func budgetRequest(i int, input, output int64) budget.Request {
	return budget.Request{ID: fmt.Sprintf("50000000-0000-4000-8000-%012d", i), Digest: strings.Repeat("d", 64), API: "openai-completions", Provider: "fixture", Model: "text-model", InputEstimate: input, MaxOutput: output}
}
func measuredRequest(id string, input, output int64) budget.Settlement {
	zero, total := int64(0), input+output
	return budget.Settlement{ID: id, State: budget.Settled, Terminal: true, Tokens: model.TokenCounts{Input: &input, Output: &output, CacheRead: &zero, CacheWrite: &zero, Total: &total}}
}
func settleRequest(t *testing.T, s *Store, token, rid string, v budget.Settlement) {
	t.Helper()
	if err := s.BeginRequestOwned(token, rid, budget.Begin{ID: v.ID, Digest: strings.Repeat("e", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleRequestOwned(token, rid, v); err != nil {
		t.Fatal(err)
	}
}

func TestRequestBudgetReservationsSettlementAndOneTimeSend(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 3)
	r := budgetRequest(1, 100, 1200)
	g, err := s.ReserveRequestOwned(l.Token, p.RunID, r)
	if err != nil || g.MaxOutput != 900 || g.ReservedTokens != 1000 {
		t.Fatal(g, err)
	}
	if again, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil || again != g {
		t.Fatal("reserve dedup", again, err)
	}
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(2, 1, 1)); !errors.Is(err, budget.ErrDenied) {
		t.Fatal("reserved funds overspent", err)
	}
	b := budget.Begin{ID: r.ID, Digest: strings.Repeat("e", 64)}
	if err := s.BeginRequestOwned(l.Token, p.RunID, b); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRequestOwned(l.Token, p.RunID, b); !errors.Is(err, budget.ErrConflict) {
		t.Fatal("second network send authorized", err)
	}
	v := measuredRequest(r.ID, 70, 30)
	for i := 0; i < 2; i++ {
		if err := s.SettleRequestOwned(l.Token, p.RunID, v); err != nil {
			t.Fatal("idempotent settlement", err)
		}
	}
	if err := s.SettleRequestOwned(l.Token, p.RunID, measuredRequest(r.ID, 70, 31)); !errors.Is(err, budget.ErrConflict) {
		t.Fatal("receipt changed", err)
	}
	g2, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(2, 100, 1200))
	if err != nil || g2.ReservedTokens != 900 || g2.MaxOutput != 800 {
		t.Fatal("actual usage not credited exactly once", g2, err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(g2.ID, 80, 20))
	g3, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(3, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(g3.ID, 1, 1))
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(4, 1, 1)); !errors.Is(err, budget.ErrDenied) {
		t.Fatal("request count bypassed", err)
	}
	o, err := s.CloseRequestBudgetOwned(l.Token, p.RunID)
	if err != nil || !o.Confirmed || o.Overrun || o.Usage.Tokens.Total == nil || *o.Usage.Tokens.Total != 202 || o.Usage.EstimatedCostUsd != nil {
		t.Fatal(o, err)
	}
}

func TestRequestBudgetConcurrentReservationIsAtomic(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 256)
	var wg sync.WaitGroup
	results := make(chan budget.Grant, 30)
	errs := make(chan error, 30)
	for i := 1; i <= 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g, e := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(i, 100, 100))
			if e == nil {
				results <- g
			} else {
				errs <- e
			}
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	var total int64
	for g := range results {
		total += g.ReservedTokens
	}
	for err := range errs {
		if !errors.Is(err, budget.ErrDenied) {
			t.Fatal(err)
		}
	}
	if total != 1000 {
		t.Fatal("concurrent ledger overspent or lost reservation", total)
	}
}

func TestRequestBudgetTaskQuotaCarriesAcrossRuns(t *testing.T) {
	s, l, ss, p := budgetFixture(t, 1000, 2)
	r := budgetRequest(1, 100, 700)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(r.ID, 500, 100))
	if _, err := s.CloseRequestBudgetOwned(l.Token, p.RunID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSessionRunOwned(l.Token, ss, p.RunID, func(st *model.State) error {
		st.Runs[0].State = model.RunSucceeded
		u := measuredRequest(r.ID, 500, 100).Tokens
		st.Runs[0].Usage = &model.Usage{Tokens: u, UsageCompleteness: model.UsageComplete}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.RunID = "40000000-0000-4000-8000-000000000005"
	if err := s.StartSessionRunOwned(l.Token, ss, p.RunID, false, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: p.RunID, TaskID: p.TaskID, ProfileID: p.ProfileID, Role: "developer", State: model.RunRunning, UpdatedAt: now()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenRequestBudgetOwned(l.Token, p); err != nil {
		t.Fatal(err)
	}
	g, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(2, 100, 900))
	if err != nil || g.ReservedTokens != 400 || g.MaxOutput != 300 {
		t.Fatal("prior ledger duplicated or lost", g, err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(g.ID, 10, 10))
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(3, 1, 1)); !errors.Is(err, budget.ErrDenied) {
		t.Fatal("request quota reset at new run", err)
	}
}

func TestRequestBudgetUnknownOverrunAndRestartNeverRefund(t *testing.T) {
	for _, mode := range []string{"missing", "unknown", "overrun", "partial-overrun"} {
		t.Run(mode, func(t *testing.T) {
			s, l, _, p := budgetFixture(t, 1000, 256)
			r := budgetRequest(1, 100, 900)
			if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
				t.Fatal(err)
			}
			if mode != "missing" {
				v := budget.Settlement{ID: r.ID, State: budget.Unknown}
				if mode == "overrun" {
					v = measuredRequest(r.ID, 1000, 30)
				}
				if mode == "partial-overrun" {
					n := int64(1100)
					v.Tokens.Input = &n
				}
				settleRequest(t, s, l.Token, p.RunID, v)
			}
			if _, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(2, 1, 1)); !errors.Is(err, budget.ErrDenied) {
				t.Fatal("unknown refunded", err)
			}
			if err := s.ReconcileRequestBudgetsOwned(l.Token); err != nil {
				t.Fatal(err)
			}
			o, err := s.CloseRequestBudgetOwned(l.Token, p.RunID)
			if err != nil || o.Confirmed || (mode == "overrun" || mode == "partial-overrun") != o.Overrun {
				t.Fatal(o, err)
			}
			if (mode == "missing" || mode == "unknown") && o.Usage.Tokens.Total != nil {
				t.Fatal("unknown became zero", o)
			}
			st, _ := s.Read()
			if st.Tasks[0].State != model.TaskUnknown || st.Runs[0].State != model.RunUnknown {
				t.Fatal("restart could replay", st.Tasks[0], st.Runs[0])
			}
			rows, err := s.RequestBudgetRecords(p.RunID)
			if err != nil || len(rows) != 1 || budgetCharge(rows[0]) < 1000 {
				t.Fatal("restart released reservation", rows, err)
			}
		})
	}
}

func TestRequestBudgetFencingFrozenContractAndCancellation(t *testing.T) {
	s, l, _, p := budgetFixture(t, 1000, 256)
	r := budgetRequest(1, 100, 100)
	if _, err := s.ReserveRequestOwned("foreign", p.RunID, r); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	bad := r
	bad.Model = "other"
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, bad); !errors.Is(err, budget.ErrDenied) {
		t.Fatal(err)
	}
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	positive := int64(1)
	if err := s.SettleRequestOwned(l.Token, p.RunID, budget.Settlement{ID: r.ID, State: budget.Canceled, Tokens: model.TokenCounts{Total: &positive}}); err == nil {
		t.Fatal("positive canceled receipt refunded")
	}
	if err := s.SettleRequestOwned(l.Token, p.RunID, budget.Settlement{ID: r.ID, State: budget.Canceled}); err != nil {
		t.Fatal(err)
	}
	if g, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(2, 100, 900)); err != nil || g.ReservedTokens != 1000 {
		t.Fatal(g, err)
	}
	if err := s.UpdateOwned(l.Token, func(st *model.State) error { st.Profiles[0].Model = "changed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRequestOwned(l.Token, p.RunID, budget.Begin{ID: budgetRequest(2, 0, 0).ID, Digest: strings.Repeat("f", 64)}); !errors.Is(err, budget.ErrDenied) {
		t.Fatal("changed profile authorized", err)
	}
}

func TestRequestBudgetDeadlineLegacyUsageAndRunCeiling(t *testing.T) {
	for _, mode := range []string{"deadline", "legacy", "run-ceiling"} {
		t.Run(mode, func(t *testing.T) {
			s, l, _, p := budgetFixture(t, 1000, 256)
			if mode == "legacy" {
				if err := s.UpdateOwned(l.Token, func(st *model.State) error {
					st.Runs = append(st.Runs, model.Run{ID: "old", TaskID: p.TaskID, State: model.RunSucceeded, Usage: &model.Usage{UsageCompleteness: model.UsageUnknown}})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				if mode == "deadline" {
					p.Deadline = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
				} else {
					p.RunTokens = 150
				}
				if _, err := s.db.Exec("UPDATE budget_runs SET payload=? WHERE run_id=?", mustJSON(p), p.RunID); err != nil {
					t.Fatal(err)
				}
			}
			g, err := s.ReserveRequestOwned(l.Token, p.RunID, budgetRequest(1, 100, 900))
			if mode == "run-ceiling" {
				if err != nil || g.MaxOutput != 50 {
					t.Fatal(g, err)
				}
			} else if err == nil {
				t.Fatal("unsafe preflight passed")
			}
		})
	}
}

func TestRequestBudgetBackupRestoreAndTamperedOverrun(t *testing.T) {
	s, l, ss, p := budgetFixture(t, 1000, 256)
	r := budgetRequest(1, 100, 100)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(r.ID, 300, 30))
	if _, err := s.CloseRequestBudgetOwned(l.Token, p.RunID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSessionRunOwned(l.Token, ss, p.RunID, func(st *model.State) error {
		st.Runs[0].State = model.RunStopped
		st.Tasks[0].State = model.TaskStopped
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bk := filepath.Join(t.TempDir(), "budget.db")
	if err := s.Backup(bk); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := Restore(bk, dst); err != nil {
		t.Fatal(err)
	}
	got, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	rows, err := got.RequestBudgetRecords(p.RunID)
	if err != nil || len(rows) != 1 || !rows[0].Overrun || *rows[0].Settlement.Tokens.Total != 330 {
		t.Fatal(rows, err)
	}
	rows[0].Overrun = false
	editBackup(t, bk, "UPDATE budget_requests SET payload=? WHERE id=?", mustJSON(rows[0]), r.ID)
	if err := ValidateBackup(bk); !errors.Is(err, ErrBadBackup) {
		t.Fatal("tampered receipt accepted", err)
	}
}
