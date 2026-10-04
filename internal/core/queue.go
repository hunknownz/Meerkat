package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

// Dispatch persists selected frozen tasks before acknowledging acceptance.
// Waiting and execution are separate: caller cancellation never stops a task.
func (c *Core) Dispatch(req model.DispatchRequest) (model.DispatchReceipt, error) {
	return c.submit(req, "workflow")
}

func (c *Core) submit(req model.DispatchRequest, mode string) (model.DispatchReceipt, error) {
	var receipt model.DispatchReceipt
	if !uuidRE.MatchString(req.RequestID) || len(req.TaskIDs) < 1 || len(req.TaskIDs) > 50 {
		return receipt, invalid("requestId and 1..50 lowercase task UUIDs are required")
	}
	req.TaskIDs = slices.Clone(req.TaskIDs)
	canonicalIDs := slices.Clone(req.TaskIDs)
	slices.Sort(canonicalIDs)
	for i, id := range canonicalIDs {
		if !uuidRE.MatchString(id) || i > 0 && id == canonicalIDs[i-1] {
			return receipt, invalid("taskIds must be unique lowercase UUIDs")
		}
	}
	if c.isClosed() {
		return receipt, ErrClosed
	}
	if c.isLost() {
		return receipt, ErrLeaseLost
	}
	// Order is part of the request: same-worktree tasks can share a baseline,
	// so reversing their order changes which one may produce the first commit.
	b, _ := json.Marshal([]any{mode, req.TaskIDs, req.Resume, req.Acknowledge})
	digest := fmt.Sprintf("%x", sha256.Sum256(b))
	old, err := c.st.OperationByRequest(req.RequestID)
	if err == nil {
		if old.RequestDigest != digest {
			return receipt, invalid("requestId is already bound to different input")
		}
		return model.DispatchReceipt{Accepted: true, Duplicate: true, Operation: old.Public()}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return receipt, err
	}
	set, err := c.settings()
	if err != nil {
		return receipt, err
	}
	// Prepared tasks own their profiles. Future defaults never rewrite them.
	set.DefaultProfiles = nil
	st, err := c.st.Read()
	if err != nil {
		return receipt, err
	}
	ack, states, err := c.validateSelection(st, set, req.TaskIDs, req.Resume, req.Acknowledge, mode == "delegate")
	if err != nil {
		// Another identical submission may have committed between the initial
		// lookup and selection. Its new queued/running state is not a conflict.
		if old, lookupErr := c.st.OperationByRequest(req.RequestID); lookupErr == nil && old.RequestDigest == digest {
			return model.DispatchReceipt{Accepted: true, Duplicate: true, Operation: old.Public()}, nil
		}
		return receipt, err
	}
	at := now()
	o := model.Operation{SchemaVersion: 1, ID: newUUID(), RequestID: req.RequestID, RequestDigest: digest,
		State: model.OperationQueued, Mode: mode, CreatedAt: at, UpdatedAt: at, FrozenFixRounds: set.MaxFixRounds, Tasks: []model.OperationTask{}}
	for _, id := range req.TaskIDs {
		t := findTask(st, id)
		if _, why := frozenOK(st, *t); why != "" {
			return receipt, invalid("task frozen contract is invalid (%s)", why)
		}
		o.Tasks = append(o.Tasks, model.OperationTask{TaskID: id, State: model.MemberQueued, ContractDigest: taskContract(st, *t),
			TaskState: model.TaskQueued, CandidateSHA: t.CandidateSha, ResumeRole: t.ResumeRole})
	}
	c.wmu.Lock()
	if c.isClosed() {
		c.wmu.Unlock()
		return receipt, ErrClosed
	}
	if c.isLost() {
		c.wmu.Unlock()
		return receipt, ErrLeaseLost
	}
	saved, duplicate, err := c.st.EnqueueOwned(c.token, o, func(s *model.State) error {
		for _, m := range o.Tasks {
			t := findTask(s, m.TaskID)
			if t == nil || t.State != states[t.ID] || taskContract(s, *t) != m.ContractDigest || deref(t.CandidateSha) != deref(m.CandidateSHA) {
				return invalid("task changed during dispatch")
			}
			// A newly observed unknown sibling must not be overwritten or bypassed.
			for _, other := range s.Tasks {
				if other.ID != t.ID && other.Worktree == t.Worktree && other.State == model.TaskUnknown {
					return invalid("worktree is occupied by an unresolved task")
				}
			}
			t.State, t.StateReason, t.UpdatedAt = model.TaskQueued, sp("dispatch_queue"), at
		}
		for _, rid := range ack {
			if r := findRun(s, rid); r != nil && r.State == model.RunUnknown {
				r.State, r.EndedAt = model.RunInterrupted, sp(at)
				pushEvent(r, "state", "interruption_acknowledged")
			}
		}
		return nil
	})
	c.wmu.Unlock()
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	if err != nil {
		return receipt, err
	}
	c.wakeQueue()
	return model.DispatchReceipt{Accepted: true, Duplicate: duplicate, Operation: saved.Public()}, nil
}

// taskContract includes frozen task and profile records but excludes execution
// progress. It is checked before every role, not just when entering the queue.
func taskContract(st *model.State, t model.Task) string {
	return model.FrozenTaskDigest(st, t)
}

func (c *Core) wakeQueue() {
	select {
	case c.queueKick <- struct{}{}:
	default:
	}
}

func (c *Core) Operation(id string) (model.Operation, error) {
	if !uuidRE.MatchString(id) {
		return model.Operation{}, invalid("operationId must be a lowercase UUID")
	}
	o, err := c.st.Operation(id)
	if err != nil {
		return model.Operation{}, err
	}
	return c.operationProgress(o)
}

// OperationByRequest recovers a persisted handle after its first reply was lost.
func (c *Core) OperationByRequest(id string) (model.Operation, error) {
	if !uuidRE.MatchString(id) {
		return model.Operation{}, invalid("requestId must be a lowercase UUID")
	}
	o, err := c.st.OperationByRequest(id)
	if err != nil {
		return model.Operation{}, err
	}
	return c.operationProgress(o)
}

func (c *Core) operationProgress(o model.Operation) (model.Operation, error) {
	o = o.Public()
	if model.OperationSettled(o.State) {
		return o, nil
	}
	st, err := c.st.Read()
	if err != nil {
		return model.Operation{}, err
	}
	for i := range o.Tasks {
		m := &o.Tasks[i]
		if m.State != model.MemberQueued && m.State != model.MemberRunning {
			continue
		}
		if t := findTask(st, m.TaskID); t != nil {
			m.TaskState, m.StateReason, m.CandidateSHA, m.ResumeRole = t.State, t.StateReason, t.CandidateSha, t.ResumeRole
		}
	}
	return o, nil
}

// WaitOperation returns a complete snapshot on timeout. It never changes the
// operation or cancels execution. Public waits are bounded to thirty seconds.
func (c *Core) WaitOperation(ctx context.Context, id string, wait time.Duration) (model.Operation, error) {
	if wait < 0 || wait > 30*time.Second {
		return model.Operation{}, invalid("waitMillis must be 0..30000")
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(min(c.opts.Poll, 50*time.Millisecond))
	defer tick.Stop()
	for {
		o, err := c.Operation(id)
		if err != nil || model.OperationSettled(o.State) || wait == 0 {
			return o, err
		}
		select {
		case <-ctx.Done():
			return o, ctx.Err()
		case <-deadline.C:
			return o, nil
		case <-tick.C:
		}
	}
}

func operationResult(o model.Operation) Result {
	r := Result{Tasks: []TaskResult{}}
	if o.Mode == "delegate" {
		r.Mode = "delegate"
	}
	if o.State == model.OperationUnknown {
		r.Fatal = "dispatch_unknown"
	}
	for _, m := range o.Tasks {
		r.Tasks = append(r.Tasks, TaskResult{ID: m.TaskID, State: m.TaskState, StateReason: m.StateReason, CandidateSha: m.CandidateSHA, ResumeRole: m.ResumeRole})
	}
	return r
}

func (c *Core) waitResult(ctx context.Context, id string) (Result, error) {
	for {
		o, err := c.WaitOperation(ctx, id, 100*time.Millisecond)
		if err != nil {
			return Result{}, err
		}
		if model.OperationSettled(o.State) {
			return operationResult(o), nil
		}
		if c.isLost() {
			r := operationResult(o)
			r.Fatal = "controller_lost"
			return r, nil
		}
		if c.isClosed() {
			r := operationResult(o)
			r.Stopped = "controller_stopped"
			return r, nil
		}
	}
}

func (c *Core) queueUpdate(id string, fn func(*model.Operation, *model.State) error) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	err := c.st.UpdateOperationOwned(c.token, id, fn)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return err
}

// reconcileQueue is called before starting the controller loops. A started
// unfinished dispatch is uncertain and is never replayed, even without a PID.
func (c *Core) reconcileQueue() error {
	ops, err := c.st.ActiveOperations()
	if err != nil {
		return err
	}
	for _, op := range ops {
		err := c.queueUpdate(op.ID, func(o *model.Operation, s *model.State) error {
			at := now()
			o.UpdatedAt = at
			if o.StartedAt == nil {
				for _, m := range o.Tasks {
					t := findTask(s, m.TaskID)
					if t == nil || t.State == model.TaskUnknown || taskContract(s, *t) != m.ContractDigest || deref(t.CandidateSha) != deref(m.CandidateSHA) {
						return invalid("unstarted queue contract changed; refusing recovery")
					}
					t.State, t.StateReason, t.UpdatedAt = model.TaskQueued, sp("dispatch_queue"), at
				}
				return nil
			}
			o.State, o.Reason, o.EndedAt = model.OperationUnknown, sp("controller_restart_unverified"), sp(at)
			for i := range o.Tasks {
				m := &o.Tasks[i]
				if m.State == model.MemberCompleted {
					continue
				}
				m.State, m.StateReason, m.EndedAt = model.MemberUnknown, sp("controller_restart_unverified"), sp(at)
				if t := findTask(s, m.TaskID); t != nil {
					if m.StartedAt != nil {
						t.State = model.TaskUnknown
					} else {
						t.State = model.TaskStopped
					}
					t.StateReason, t.UpdatedAt = m.StateReason, at
					m.TaskState, m.CandidateSHA, m.ResumeRole = t.State, t.CandidateSha, t.ResumeRole
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type queuedMember struct {
	operation model.Operation
	member    model.OperationTask
}
type queueDone struct {
	operationID, taskID string
	slot                int
}

// queueLoop is the only dispatcher, including for legacy Execute/Delegate.
// Across all submissions, tasks share slots and worktree exclusion.
func (c *Core) queueLoop() {
	defer c.bg.Done()
	tick := time.NewTicker(c.opts.Poll)
	defer tick.Stop()
	running := map[string]active{}
	slots := make([]bool, model.MaxConcurrency)
	done := make(chan queueDone, model.MaxConcurrency)
	for {
		closing := c.isClosed() || c.isLost()
		if closing && len(running) == 0 {
			return
		}
		if !closing {
			ops, err := c.st.ActiveOperations()
			st, serr := c.st.Read()
			set, seterr := c.settings()
			if err == nil && serr == nil && seterr == nil {
				members := []queuedMember{}
				pending, selected := []string{}, []string{}
				for _, op := range ops {
					for _, m := range op.Tasks {
						selected = append(selected, m.TaskID)
						if m.State == model.MemberQueued {
							members = append(members, queuedMember{op, m})
							pending = append(pending, m.TaskID)
						}
					}
				}
				for _, item := range members {
					if c.isClosed() || c.isLost() {
						break
					}
					m, op := item.member, item.operation
					t := findTask(st, m.TaskID)
					if t == nil {
						continue
					} // foreign key validation makes this an unreadable store
					if t.State != model.TaskQueued || taskContract(st, *t) != m.ContractDigest || deref(t.CandidateSha) != deref(m.CandidateSHA) {
						_ = c.finishMember(op.ID, m.TaskID, "dispatch_contract_changed", false)
						continue
					}
					if c.checkpointOccupied(*t) {
						_ = c.finishMember(op.ID, m.TaskID, "worktree_reserved_checkpoint", true)
						continue
					}
					if slices.ContainsFunc(st.Tasks, func(other model.Task) bool {
						return other.ID != t.ID && other.Worktree == t.Worktree && other.State == model.TaskUnknown
					}) {
						_ = c.finishMember(op.ID, m.TaskID, "worktree_occupied_unknown", true)
						continue
					}
					block, wait := c.depStatus(st, *t, pending, running, selected)
					if block != "" {
						_ = c.finishMember(op.ID, m.TaskID, block, true)
						continue
					}
					busy := false
					for _, a := range running {
						busy = busy || a.worktree == t.Worktree
					}
					reason := ""
					switch {
					case wait:
						reason = "dependencies"
					case busy:
						reason = "worktree"
					case projectSaturated(t.ProjectID, running, set):
						reason = "project_concurrency"
					case providerSaturated(taskProviders(st, *t), running, set):
						reason = "provider_concurrency"
					case len(running) >= set.MaxConcurrency:
						reason = "concurrency"
					}
					if reason != "" {
						if deref(t.StateReason) != reason {
							_ = c.update(func(s *model.State) error {
								if cur := findTask(s, t.ID); cur != nil && cur.State == model.TaskQueued {
									cur.StateReason, cur.UpdatedAt = sp(reason), now()
								}
								return nil
							})
						}
						continue
					}
					err := c.queueUpdate(op.ID, func(o *model.Operation, s *model.State) error {
						if c.isClosed() {
							return ErrClosed
						}
						cur := findTask(s, m.TaskID)
						if cur == nil || cur.State != model.TaskQueued || taskContract(s, *cur) != m.ContractDigest || deref(cur.CandidateSha) != deref(m.CandidateSHA) {
							return invalid("task changed before start")
						}
						if slices.ContainsFunc(s.Tasks, func(other model.Task) bool {
							return other.ID != cur.ID && other.Worktree == cur.Worktree && other.State == model.TaskUnknown
						}) {
							return invalid("worktree identity is unknown")
						}
						at := now()
						for i := range o.Tasks {
							if o.Tasks[i].TaskID == m.TaskID {
								if o.Tasks[i].State != model.MemberQueued {
									return invalid("member is not queued")
								}
								o.Tasks[i].State, o.Tasks[i].StartedAt = model.MemberRunning, sp(at)
							}
						}
						if o.StartedAt == nil {
							o.StartedAt = sp(at)
						}
						o.State, o.UpdatedAt = model.OperationRunning, at
						return nil
					})
					if err != nil {
						continue
					}
					slot := slices.Index(slots, false)
					slots[slot] = true
					running[t.ID] = active{worktree: t.Worktree, projectID: t.ProjectID, providers: taskProviders(st, *t)}
					at, _ := time.Parse(time.RFC3339Nano, op.CreatedAt)
					qw := queueWait{at: at, until: time.Now()}
					go func(op model.Operation, m model.OperationTask, slot int, qw queueWait) {
						c.runTask(context.Background(), m.TaskID, fmt.Sprintf("Agent-%02d", slot+1), qw, &op)
						done <- queueDone{op.ID, m.TaskID, slot}
					}(op, m, slot, qw)
				}
			}
		}
		c.mu.Lock()
		c.dispatching = len(running) > 0
		c.mu.Unlock()
		select {
		case d := <-done:
			// Keep the slot until its outcome has been durably recorded.
			if err := c.finishMember(d.operationID, d.taskID, "", false); err != nil && !c.isLost() {
				// The execution has returned but its outcome is not confirmed.
				// Stop this owner rather than dispatch more work after a write failure.
				c.loseLease()
			}
			delete(running, d.taskID)
			slots[d.slot] = false
		case <-tick.C:
		case <-c.queueKick:
		}
	}
}

func (c *Core) finishMember(opID, taskID, why string, blocked bool) error {
	return c.queueUpdate(opID, func(o *model.Operation, s *model.State) error {
		i := slices.IndexFunc(o.Tasks, func(m model.OperationTask) bool { return m.TaskID == taskID })
		if i < 0 {
			return invalid("operation member missing")
		}
		m := &o.Tasks[i]
		if m.State == model.MemberCompleted || m.State == model.MemberUnknown {
			return nil
		}
		t := findTask(s, taskID)
		if t == nil {
			return invalid("operation task missing")
		}
		at := now()
		if why != "" && t.State == model.TaskQueued {
			t.State, t.StateReason, t.UpdatedAt = model.TaskFailed, sp(why), at
			if blocked {
				t.State = model.TaskBlocked
			}
		}
		m.State = model.MemberCompleted
		if model.IsActiveTaskState(t.State) && !isDelegateCandidate(*t) || t.State == model.TaskUnknown || t.State == model.TaskQueued {
			m.State = model.MemberUnknown
			if t.State != model.TaskUnknown {
				t.RecordedState, t.State, t.StateReason, t.UpdatedAt = t.State, model.TaskUnknown, sp("task_outcome_unverified"), at
			}
		}
		m.TaskState, m.StateReason, m.CandidateSHA, m.ResumeRole, m.EndedAt = t.State, t.StateReason, t.CandidateSha, t.ResumeRole, sp(at)
		o.UpdatedAt = at
		// A preflight outcome starts processing the operation even if no executor
		// has started yet. Keep a mixed completed/queued batch durably running.
		if o.StartedAt == nil {
			o.StartedAt = sp(at)
		}
		o.State = model.OperationRunning
		all, unknown := true, false
		for _, member := range o.Tasks {
			all = all && (member.State == model.MemberCompleted || member.State == model.MemberUnknown)
			unknown = unknown || member.State == model.MemberUnknown
		}
		if all {
			o.State, o.EndedAt = model.OperationCompleted, sp(at)
			if unknown {
				o.State, o.Reason = model.OperationUnknown, sp("task_outcome_unverified")
			}
		}
		return nil
	})
}

// Reserve every frozen role provider for the whole task; a role transition
// cannot silently oversubscribe a provider. This is a task cap, not a key quota.
func taskProviders(st *model.State, t model.Task) []string {
	out := []string{}
	for _, id := range t.ProfileIDs {
		for _, p := range st.Profiles {
			if p.ID == id && !slices.Contains(out, p.Provider) {
				out = append(out, p.Provider)
			}
		}
	}
	slices.Sort(out)
	return out
}
func projectSaturated(id string, running map[string]active, set model.Settings) bool {
	cap, ok := set.ProjectConcurrency[id]
	if !ok {
		return false
	}
	n := 0
	for _, a := range running {
		if a.projectID == id {
			n++
		}
	}
	return n >= cap
}
func providerSaturated(ids []string, running map[string]active, set model.Settings) bool {
	for _, id := range ids {
		cap, ok := set.ProviderConcurrency[id]
		if !ok {
			continue
		}
		n := 0
		for _, a := range running {
			if slices.Contains(a.providers, id) {
				n++
			}
		}
		if n >= cap {
			return true
		}
	}
	return false
}
