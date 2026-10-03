package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

func TestBudgetDecisionRequestGateKeepsHistoricalRevision(t *testing.T) {
	s, l, ss, p := budgetFixture(t, 1000, 256)
	r := budgetRequest(1, 100, 800)
	if _, err := s.ReserveRequestOwned(l.Token, p.RunID, r); err != nil {
		t.Fatal(err)
	}
	settleRequest(t, s, l.Token, p.RunID, measuredRequest(r.ID, 500, 300))
	if _, err := s.CloseRequestBudgetOwned(l.Token, p.RunID); err != nil {
		t.Fatal(err)
	}
	ss.State = model.SessionIdle
	ss.ActiveRunID = nil
	if err := s.FinishSessionRunOwned(l.Token, ss, p.RunID, func(st *model.State) error {
		st.Runs[0].State = model.RunStopped
		st.Tasks[0].State = model.TaskStopped
		why := "budget_tokens"
		st.Tasks[0].StateReason = &why
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	proposal, err := s.ProposeBudget(model.BudgetIncrease{TaskID: p.TaskID, AddTokens: 500, Reason: "Complete bounded fixture"})
	if err != nil {
		t.Fatal(err)
	}
	d := model.BudgetDecisionInput{Proposal: proposal, RequestID: "88000000-0000-4000-8000-000000000001", AuthorizationRef: "fixture explicit authorization", Apply: true}
	if _, err := s.ApplyBudgetDecisionOwned(l.Token, d); err != nil {
		t.Fatal(err)
	}
	rid := "99000000-0000-4000-8000-000000000001"
	if err := s.StartSessionRunOwned(l.Token, ss, rid, false, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: rid, TaskID: p.TaskID, ProfileID: p.ProfileID, State: model.RunRunning})
		st.Tasks[0].State = model.TaskImplementing
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldPolicy := p
	oldPolicy.RunID = rid
	oldPolicy.RunTokens = 200
	if err := s.OpenRequestBudgetOwned(l.Token, oldPolicy); !errors.Is(err, budget.ErrDenied) {
		t.Fatal("stale revision authorized", err)
	}
	p2 := p
	p2.RunID = rid
	p2.TaskTokens = 1500
	p2.RunTokens = 700
	p2.BudgetRevision = 1
	if err := s.OpenRequestBudgetOwned(l.Token, p2); err != nil {
		t.Fatal(err)
	}
	g, err := s.ReserveRequestOwned(l.Token, rid, budgetRequest(2, 100, 800))
	if err != nil || g.ReservedTokens != 700 {
		t.Fatal("past use ignored or addition missing", g, err)
	}
	saved, err := loadBudgetPolicy(s.rdb, p.RunID)
	if err != nil || saved.TaskTokens != 1000 || saved.BudgetRevision != 0 {
		t.Fatal("old authority rewritten", saved, err)
	}
	_, ev, err := s.ExecutionEvidence(p.TaskID)
	if err != nil || ev.AuthorizedTokens != 1500 || ev.ConfirmedTokens != 800 || ev.ReservedTokens != 700 {
		t.Fatal(ev, err)
	}
	// Neither a new allowance nor a different request ID can forgive uncertainty.
	if _, err := s.CloseRequestBudgetOwned(l.Token, rid); err != nil {
		t.Fatal(err)
	}
	ss.State = model.SessionIdle
	ss.ActiveRunID = nil
	if err := s.FinishSessionRunOwned(l.Token, ss, rid, func(st *model.State) error {
		st.Runs[1].State = model.RunStopped
		st.Tasks[0].State = model.TaskStopped
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProposeBudget(model.BudgetIncrease{TaskID: p.TaskID, AddTokens: 100, Reason: "Uncertain fixture"}); !errors.Is(err, budget.ErrUnknown) {
		t.Fatal("uncertain request forgiven", err)
	}
	if _, err := s.ApplyBudgetDecisionOwned(strings.Repeat("x", 64), d); err == nil {
		t.Fatal("foreign lease applied decision")
	}
}

func TestBudgetDecisionCorruptionRejectsReceiptAndBackup(t *testing.T) {
	for _, kind := range []string{"revision", "timestamp", "contract"} {
		t.Run(kind, func(t *testing.T) {
			s, l, ss, p := budgetFixture(t, 1000, 256)
			if _, err := s.CloseRequestBudgetOwned(l.Token, p.RunID); err != nil {
				t.Fatal(err)
			}
			ss.State, ss.ActiveRunID = model.SessionIdle, nil
			if err := s.FinishSessionRunOwned(l.Token, ss, p.RunID, func(st *model.State) error {
				st.Runs[0].State = model.RunStopped
				st.Tasks[0].State = model.TaskStopped
				why := "budget_tokens"
				st.Tasks[0].StateReason = &why
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			proposal, err := s.ProposeBudget(model.BudgetIncrease{TaskID: p.TaskID, AddWallSeconds: 100, Reason: "Finish bounded checks"})
			if err != nil {
				t.Fatal(err)
			}
			input := model.BudgetDecisionInput{Proposal: proposal, RequestID: "88000000-0000-4000-8000-000000000001", AuthorizationRef: "explicit fixture authorization", Apply: true}
			d, err := s.ApplyBudgetDecisionOwned(l.Token, input)
			if err != nil {
				t.Fatal(err)
			}
			// Corrupt private persisted evidence; it must not become valid authority.
			revision := int64(1)
			switch kind {
			case "revision":
				revision = 2
			case "timestamp":
				d.CreatedAt = "unconfirmed"
			case "contract":
				d.Proposal.ContractDigest = strings.Repeat("a", 64)
			}
			if _, err := s.db.Exec("UPDATE budget_decisions SET revision=?,payload=? WHERE request_id=?", revision, mustJSON(d), input.RequestID); err != nil {
				t.Fatal(err)
			}
			if kind != "contract" {
				if _, err := s.BudgetDecision(input.RequestID); !errors.Is(err, ErrConflict) {
					t.Fatal("corrupt receipt accepted", err)
				}
				if _, err := s.ApplyBudgetDecisionOwned(l.Token, input); !errors.Is(err, ErrConflict) {
					t.Fatal("corrupt dedup receipt accepted", err)
				}
			}
			if _, _, err := s.EffectiveBudget(p.TaskID); !errors.Is(err, ErrConflict) {
				t.Fatal("corrupt allowance accepted", err)
			}
			path := filepath.Join(t.TempDir(), "corrupt.db")
			if err := s.Backup(path); !errors.Is(err, ErrBadBackup) {
				t.Fatal("corrupt backup accepted", err)
			}
			mustNotExist(t, path)
		})
	}
}
