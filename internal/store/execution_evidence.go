package store

import (
	"errors"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

// ExecutionEvidence reads one task's projections in the same SQLite snapshot.
func (s *Store) ExecutionEvidence(taskID string) ([]model.SessionSummary, *model.BudgetEvidence, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	ss, err := sessions(tx, taskID)
	if err != nil {
		return nil, nil, err
	}
	out := []model.SessionSummary{}
	for _, v := range ss {
		out = append(out, model.SessionSummary{ID: v.ID, Role: v.Role, Executor: v.Executor, State: v.State, ActiveRunID: v.ActiveRunID, LastSHA: v.LastSHA, UpdatedAt: v.UpdatedAt})
	}
	ps, rs, err := budgetRows(tx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if len(ps) == 0 {
		return out, nil, nil
	}
	st, err := readState(tx)
	if err != nil {
		return nil, nil, err
	}
	p := ps[len(ps)-1]
	t := taskByID(st, taskID)
	if t == nil {
		return nil, nil, ErrNotFound
	}
	v, err := allowanceAt(tx, st, *t, nil)
	if err != nil {
		return nil, nil, err
	}
	p.TaskTokens = v.AuthorizedTokens // A closed older Run does not cap a later authorized addition.
	b := &model.BudgetEvidence{Mode: p.TokenMode, AuthorizedTokens: p.TaskTokens}
	for _, r := range rs {
		if r.State == budget.Canceled {
			continue
		}
		b.Requests++
		b.Overrun = b.Overrun || r.Overrun
		switch r.State {
		case budget.RateLimited:
			// Definite generation rejection is neither pending nor measured usage.
		case budget.Settled:
			b.ConfirmedTokens += budgetCharge(r)
		case budget.Unknown:
			b.UnknownRequests++
			b.ReservedTokens += budgetCharge(r)
		default:
			b.PendingRequests++
			b.ReservedTokens += budgetCharge(r)
		}
	}
	left, _, _, err := budgetRemaining(st, p, ps, rs)
	switch {
	case err == nil && p.TokenMode != "monitor":
		b.AvailableTokens = &left
	case err == nil:
	case errors.Is(err, budget.ErrDenied):
		zero := int64(0)
		b.AvailableTokens = &zero
	case errors.Is(err, budget.ErrUnknown):
		// Uncertain policy/legacy usage does not imply an available allowance.
	default:
		return nil, nil, err
	}
	b.Warning = p.TokenMode == "monitor" && b.ConfirmedTokens >= p.TaskTokens
	return out, b, nil
}
