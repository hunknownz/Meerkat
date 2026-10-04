package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

func TestBudgetRecoveryWithPartialBreakdown(t *testing.T) {
	for _, kind := range []string{"confirmed", "confirmed-with-canceled", "mismatched", "unknown-total", "unknown-completeness", "missing-requests", "unknown-request", "pending-request", "overrun", "unbacked-run"} {
		t.Run(kind, func(t *testing.T) {
			s, lease, session, policy := budgetFixture(t, 1000, 256)
			total, output := int64(700), int64(50)
			if kind == "overrun" {
				total = 950
			}
			if kind != "missing-requests" {
				req := budgetRequest(1, 100, 800)
				if _, err := s.ReserveRequestOwned(lease.Token, policy.RunID, req); err != nil {
					t.Fatal(err)
				}
				if err := s.BeginRequestOwned(lease.Token, policy.RunID, budget.Begin{ID: req.ID, Digest: req.Digest}); err != nil {
					t.Fatal(err)
				}
				if kind != "pending-request" {
					settlement := budget.Settlement{ID: req.ID, State: budget.Settled, Terminal: true, Tokens: model.TokenCounts{Total: &total, Output: &output}}
					if kind == "unknown-request" {
						settlement.State, settlement.Terminal, settlement.Tokens = budget.Unknown, false, model.TokenCounts{}
					}
					if err := s.SettleRequestOwned(lease.Token, policy.RunID, settlement); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "confirmed-with-canceled" {
				req := budgetRequest(2, 10, 10)
				if _, err := s.ReserveRequestOwned(lease.Token, policy.RunID, req); err != nil {
					t.Fatal(err)
				}
				if err := s.SettleRequestOwned(lease.Token, policy.RunID, budget.Settlement{ID: req.ID, State: budget.Canceled}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CloseRequestBudgetOwned(lease.Token, policy.RunID); err != nil {
				t.Fatal(err)
			}
			usage := &model.Usage{Tokens: model.TokenCounts{Total: &total, Output: &output}, UsageCompleteness: model.UsagePartial, Source: model.UsageSourceExecutor}
			if kind == "mismatched" {
				n := total + 1
				usage.Tokens.Total = &n
			}
			if kind == "unknown-total" {
				usage.Tokens.Total = nil
			}
			if kind == "unknown-completeness" {
				usage.UsageCompleteness = model.UsageUnknown
			}
			session.State, session.ActiveRunID = model.SessionIdle, nil
			if err := s.FinishSessionRunOwned(lease.Token, session, policy.RunID, func(st *model.State) error {
				at := now()
				st.Runs[0].State, st.Runs[0].Usage = model.RunStopped, usage
				st.Runs[0].StartedAt, st.Runs[0].EndedAt = at, &at
				st.Tasks[0].State = model.TaskStopped
				why := "budget_tokens"
				st.Tasks[0].StateReason = &why
				if kind == "unbacked-run" {
					r := st.Runs[0]
					r.ID = "99000000-0000-4000-8000-000000000009"
					st.Runs = append(st.Runs, r)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := s.Read()
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := s.ProposeBudget(model.BudgetIncrease{TaskID: policy.TaskID, AddTokens: 500, Reason: "Finish bounded fixture"})
			allowed := kind == "confirmed" || kind == "confirmed-with-canceled"
			if !allowed {
				if kind == "overrun" {
					if !errors.Is(err, budget.ErrDenied) {
						t.Fatal("overrun was not rejected", err)
					}
				} else if !errors.Is(err, budget.ErrUnknown) {
					t.Fatal("unresolved evidence accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			input := model.BudgetDecisionInput{Proposal: proposal, RequestID: "88000000-0000-4000-8000-000000000001", AuthorizationRef: "explicit fixture authorization", Apply: true}
			if _, err := s.ApplyBudgetDecisionOwned(lease.Token, input); err != nil {
				t.Fatal(err)
			}
			after, err := s.Read()
			if err != nil || !reflect.DeepEqual(before.Runs, after.Runs) {
				t.Fatal("historical usage changed", err)
			}
			_, evidence, err := s.ExecutionEvidence(policy.TaskID)
			if err != nil || evidence.ConfirmedTokens != total || evidence.AuthorizedTokens != 1500 {
				t.Fatal("prior total was lost or duplicated", evidence, err)
			}
		})
	}
}
