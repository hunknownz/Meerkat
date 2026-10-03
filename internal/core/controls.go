package core

import (
	"errors"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

// RequestWrapUp freezes one bounded instruction for the current owned Run.
// An authorization reference records the caller's evidence; it is not an
// authentication mechanism. Scope, budget and context remain frozen.
func (c *Core) RequestWrapUp(in model.WrapUpInput) (model.ControlReceipt, error) {
	if !model.ValidWrapUpInput(in) {
		return model.ControlReceipt{}, invalid("explicit wrap-up authorization required")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return model.ControlReceipt{}, ErrLeaseLost
	}
	_, oldErr := c.st.Control(in.RequestID)
	if oldErr != nil && !errors.Is(oldErr, store.ErrNotFound) {
		return model.ControlReceipt{}, oldErr
	}
	if oldErr != nil {
		c.mu.Lock()
		l := c.lanes[in.RunID]
		can := !c.closed && l != nil && l.controls != nil && !l.controlClosed && l.controlSession == in.SessionID
		c.mu.Unlock()
		if !can {
			return model.ControlReceipt{}, invalid("no owned Run supports this control and session")
		}
	}
	_, err := c.st.RequestWrapUpOwned(c.token, in)
	if err != nil {
		return model.ControlReceipt{}, err
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
	return c.st.ControlReceipt(in.RequestID)
}

func (c *Core) ControlReceipt(id string) (model.ControlReceipt, error) {
	return c.st.ControlReceipt(id)
}

func (c *Core) processControls() {
	if c.isLost() {
		return
	}
	all, err := c.st.PendingControls()
	if err != nil {
		return
	}
	for _, v := range all {
		c.mu.Lock()
		l := c.lanes[v.Input.RunID]
		if l != nil && l.controls != nil && !l.controlClosed && l.controlSession == v.Input.SessionID && l.controlID == "" {
			select {
			case l.controls <- executor.RunControl{ID: v.Input.RequestID, Kind: "wrap_up"}:
				l.controlID = v.Input.RequestID
			default:
			}
		}
		c.mu.Unlock()
	}
}

type controlAuthority struct {
	c     *Core
	runID string
}

func (a *controlAuthority) Begin(id string) error {
	c := a.c
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	v, err := c.st.Control(id)
	if err != nil || v.Input.RunID != a.runID {
		return store.ErrConflict
	}
	st, err := c.st.Read()
	if err != nil {
		return err
	}
	t := findTask(st, v.TaskID)
	r := findRun(st, a.runID)
	if t == nil || r == nil {
		return store.ErrConflict
	}
	set, err := c.settings()
	if err != nil {
		return err
	}
	_, why := frozenOK(st, *t)
	p, profileWhy := c.verifiedProfile(st, set, *t, r.Role)
	if why != "" || profileWhy != "" || taskContract(st, *t) != v.ContractDigest || profileContract(p) != v.ProfileDigest {
		if err := c.st.FinishControlOwned(c.token, id, model.ControlRejected, "", "contract_changed"); err != nil {
			return err
		}
		return store.ErrConflict
	}
	return c.st.BeginControlOwned(c.token, id)
}

func (a *controlAuthority) Finish(id, state, disposition, reason string) error {
	c := a.c
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	v, err := c.st.Control(id)
	if err != nil || v.Input.RunID != a.runID {
		return store.ErrConflict
	}
	return c.st.FinishControlOwned(c.token, id, state, disposition, reason)
}

func (c *Core) closeControls(runID string, l *lane) error {
	c.mu.Lock()
	l.controlClosed = true
	c.mu.Unlock()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	return c.st.CloseControlsOwned(c.token, runID)
}
