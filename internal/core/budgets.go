package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
	"github.com/hunknownz/Meerkat/internal/store"
)

type requestAuthority struct {
	c      *Core
	runID  string
	wrapUp chan struct{}
	once   sync.Once
}

func (a *requestAuthority) checkWrapUp() error {
	remaining, threshold, err := a.c.st.RequestHeadroomOwned(a.c.token, a.runID)
	if err != nil {
		return err
	}
	if threshold > 0 && remaining <= threshold {
		a.once.Do(func() { close(a.wrapUp) })
	}
	return nil
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
	err := a.call(ctx, func() error {
		var e error
		g, e = a.c.st.ReserveRequestOwned(a.c.token, a.runID, r)
		if e == nil {
			e = a.checkWrapUp()
		}
		return e
	})
	return g, err
}
func (a *requestAuthority) Begin(ctx context.Context, b budget.Begin) error {
	return a.call(ctx, func() error { return a.c.st.BeginRequestOwned(a.c.token, a.runID, b) })
}
func (a *requestAuthority) Settle(ctx context.Context, v budget.Settlement) error {
	return a.call(ctx, func() error {
		err := a.c.st.SettleRequestOwned(a.c.token, a.runID, v)
		if err == nil {
			err = a.checkWrapUp()
		}
		return err
	})
}

func (c *Core) openRequestBudget(t model.Task, p model.Profile, ss *model.Session, runID string, tokens, seconds int64) (*requestAuthority, error) {
	b, revision, e := c.st.EffectiveBudget(t.ID)
	if e != nil {
		return nil, e
	}
	policy := budget.Policy{RunID: runID, TaskID: t.ID, SessionID: ss.ID, ProfileID: p.ID, ProfileDigest: ss.ProfileDigest, ContractDigest: ss.ContractDigest,
		TokenMode: b.Mode,
		Provider:  p.Provider, Model: p.Model, Version: budget.PolicyVersion, Deadline: time.Now().Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339Nano),
		TaskTokens: b.MaxTokens, RunTokens: tokens, TaskRequests: budget.MaxRequests, BudgetRevision: revision, State: "open"}
	if b.StageReserves != nil {
		policy.WrapUpTokens = min(b.StageReserves.WrapUpTokens, tokens-1)
	}
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
	return &requestAuthority{c: c, runID: runID, wrapUp: make(chan struct{})}, nil
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

func (a *requestAuthority) RateLimit(ctx context.Context, v budget.RateLimitReport) (budget.RetryGrant, error) {
	var result budget.RetryGrant
	err := a.call(ctx, func() error { var e error; result, e = a.c.st.RateLimitRetryOwned(a.c.token, a.runID, v); return e })
	if err == nil && result.Retry {
		err = a.c.update(func(st *model.State) error {
			r := findRun(st, a.runID)
			if r != nil {
				pushEvent(r, "budget", "rate_limit_wait")
			}
			return nil
		})
	}
	return result, err
}
