package core

import (
	"errors"
	"reflect"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

func (c *Core) verifyBudgetTarget(taskID string) error {
	st, e := c.st.Read()
	if e != nil {
		return e
	}
	t := findTask(st, taskID)
	if t == nil {
		return store.ErrNotFound
	}
	if _, why := frozenOK(st, *t); why != "" {
		return invalid("budget task context is unverifiable")
	}
	for role := range t.ProfileIDs {
		if _, why := c.verifiedProfile(st, model.Settings{}, *t, role); why != "" {
			return invalid("budget task profile changed")
		}
	}
	cp, e := c.savedCheckpoint(taskID)
	if e != nil {
		return e
	}
	if cp != nil {
		if e := c.verifyCheckpoint(st, *t, *cp, step{role: cp.Role, purpose: cp.Purpose}); e != nil {
			return e
		}
	} else {
		if t.State == model.TaskPaused {
			return invalid("paused task has no verified checkpoint")
		}
		f := inspect(t.Worktree)
		p := pipelineOf(st, t.ID)
		if f.Err != nil || !f.Clean || f.Branch != deref(t.Branch) || f.Head != expectedHead(st, *t, p, f.Head) {
			return invalid("budget task worktree changed")
		}
	}
	ss, e := c.st.SessionsForTask(taskID)
	if e != nil {
		return e
	}
	for _, s := range ss {
		if s.State != model.SessionIdle || s.ActiveRunID != nil {
			return invalid("budget task session is unresolved")
		}
		prof, why := c.verifiedProfile(st, model.Settings{}, *t, s.Role)
		if why != "" {
			return invalid("budget task session profile changed")
		}
		x, ok := c.reg[execName(prof)].(executor.StatefulExecutor)
		if !ok {
			return invalid("budget executor cannot verify sessions")
		}
		h, e := x.InspectSession(c.sessionBinding(s, t.Worktree))
		if e != nil || h.Digest != s.FileDigest || h.ProviderID != s.ProviderID {
			return invalid("budget session history changed")
		}
	}
	return nil
}
func (c *Core) ProposeBudget(in model.BudgetIncrease) (model.BudgetProposal, error) {
	if !model.ValidBudgetIncrease(in) {
		return model.BudgetProposal{}, invalid("invalid additional budget")
	}
	if e := c.verifyBudgetTarget(in.TaskID); e != nil {
		return model.BudgetProposal{}, e
	}
	return c.st.ProposeBudget(in)
}
func budgetReceipt(d model.BudgetDecision) model.BudgetDecisionSummary {
	p := d.Proposal
	return model.BudgetDecisionSummary{RequestID: d.RequestID, Revision: p.Revision + 1, AddTokens: p.AddTokens, AddWallSeconds: p.AddWallSeconds, Reason: p.Reason, CreatedAt: d.CreatedAt}
}
func (c *Core) ApplyBudgetDecision(in model.BudgetDecisionInput) (model.BudgetDecisionSummary, error) {
	if !model.ValidBudgetDecision(in) {
		return model.BudgetDecisionSummary{}, invalid("explicit apply and authorization reference required")
	}
	old, e := c.st.BudgetDecision(in.RequestID)
	if e == nil {
		if !reflect.DeepEqual(old.BudgetDecisionInput, in) {
			return model.BudgetDecisionSummary{}, invalid("request ID already belongs to another budget decision")
		}
		return budgetReceipt(old), nil
	}
	if !errors.Is(e, store.ErrNotFound) {
		return model.BudgetDecisionSummary{}, e
	}
	if e := c.verifyBudgetTarget(in.Proposal.TaskID); e != nil {
		return model.BudgetDecisionSummary{}, e
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return model.BudgetDecisionSummary{}, ErrLeaseLost
	}
	d, e := c.st.ApplyBudgetDecisionOwned(c.token, in)
	if errors.Is(e, store.ErrLeaseLost) {
		c.loseLease()
	}
	return budgetReceipt(d), e
}
func (c *Core) BudgetDecision(requestID string) (model.BudgetDecisionSummary, error) {
	d, e := c.st.BudgetDecision(requestID)
	if e != nil {
		return model.BudgetDecisionSummary{}, e
	}
	return budgetReceipt(d), nil
}
func ParseBudgetProposal(raw []byte) (model.BudgetProposal, error) {
	var p model.BudgetProposal
	e := decodeStrict(raw, &p, 16384)
	if e != nil {
		return p, e
	}
	if !model.ValidBudgetProposal(p) {
		return p, invalid("invalid budget proposal")
	}
	return p, nil
}
