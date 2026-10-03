package core

import (
	"context"
	"errors"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
	"github.com/hunknownz/Meerkat/internal/store"
)

type requestAuthority struct {
	c     *Core
	runID string
}

func (a *requestAuthority) call(ctx context.Context, fn func() error) error {
	if ctx.Err() != nil {
		return budget.ErrUnknown
	}
	a.c.wmu.Lock()
	defer a.c.wmu.Unlock()
	if a.c.isLost() {
		return ErrLeaseLost
	}
	err := fn()
	if errors.Is(err, store.ErrLeaseLost) {
		a.c.loseLease()
	}
	return err
}
func (a *requestAuthority) Reserve(ctx context.Context, r budget.Request) (budget.Grant, error) {
	var g budget.Grant
	err := a.call(ctx, func() error { var e error; g, e = a.c.st.ReserveRequestOwned(a.c.token, a.runID, r); return e })
	return g, err
}
func (a *requestAuthority) Begin(ctx context.Context, b budget.Begin) error {
	return a.call(ctx, func() error { return a.c.st.BeginRequestOwned(a.c.token, a.runID, b) })
}
func (a *requestAuthority) Settle(ctx context.Context, v budget.Settlement) error {
	return a.call(ctx, func() error { return a.c.st.SettleRequestOwned(a.c.token, a.runID, v) })
}

func (c *Core) openRequestBudget(t model.Task, p model.Profile, ss *model.Session, runID string, tokens, seconds int64) (*requestAuthority, error) {
	b := model.DefaultBudget()
	if t.Budget != nil {
		b = *t.Budget
	}
	policy := budget.Policy{RunID: runID, TaskID: t.ID, SessionID: ss.ID, ProfileID: p.ID, ProfileDigest: ss.ProfileDigest, ContractDigest: ss.ContractDigest,
		Provider: p.Provider, Model: p.Model, Version: budget.PolicyVersion, Deadline: time.Now().Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339Nano),
		TaskTokens: b.MaxTokens, RunTokens: tokens, TaskRequests: budget.MaxRequests, State: "open"}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return nil, ErrLeaseLost
	}
	err := c.st.OpenRequestBudgetOwned(c.token, policy)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	if err != nil {
		return nil, err
	}
	return &requestAuthority{c: c, runID: runID}, nil
}

func (c *Core) closeRequestBudget(runID string) (budget.Outcome, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return budget.Outcome{}, ErrLeaseLost
	}
	o, err := c.st.CloseRequestBudgetOwned(c.token, runID)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return o, err
}
