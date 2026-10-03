package core

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"syscall"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

func (c *Core) stageAndFinishCompletion(id string, finalSession model.Session, process *executor.Process, settle func(*model.State) error) error {
	before, err := c.st.Read()
	if err != nil {
		return err
	}
	r := findRun(before, id)
	if r == nil {
		return store.ErrNotFound
	}
	t := findTask(before, r.TaskID)
	if t == nil {
		return store.ErrNotFound
	}
	ss, err := c.st.SessionForRun(id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(before)
	if err != nil {
		return err
	}
	var after model.State
	if json.Unmarshal(raw, &after) != nil {
		return invalid("completion state could not be prepared")
	}
	if err := settle(&after); err != nil {
		return err
	}
	final := findRun(&after, id)
	if final == nil || final.State != model.RunSucceeded {
		return c.finishRoleRecord(&finalSession, id, settle)
	}
	v := model.CompletionEvidence{SchemaVersion: 1, ContractDigest: taskContract(before, *t), BeforeTask: *t, BeforeRun: *r, BeforeSession: ss,
		AfterTask: *findTask(&after, t.ID), AfterRun: *final, AfterSession: finalSession, CreatedAt: now(), Deliveries: []model.Delivery{}, Reviews: []model.Review{}}
	if process != nil {
		v.ProcessGroupID = process.PGID
	}
	for _, d := range after.Deliveries {
		if !slices.ContainsFunc(before.Deliveries, func(x model.Delivery) bool { return x.ID == d.ID }) {
			v.Deliveries = append(v.Deliveries, d)
		}
	}
	for _, r := range after.Reviews {
		if !slices.ContainsFunc(before.Reviews, func(x model.Review) bool { return x.ID == r.ID }) {
			v.Reviews = append(v.Reviews, r)
		}
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	if err := c.st.StageCompletionOwned(c.token, v); err != nil {
		if errors.Is(err, store.ErrLeaseLost) {
			c.loseLease()
		}
		return err
	}
	err = c.st.FinishCompletionOwned(c.token, id)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return err
}

// InspectRecovery never changes a task or probes its executor with a prompt.
func (c *Core) InspectRecovery(taskID string) (model.RecoveryInspection, error) {
	out := model.RecoveryInspection{SchemaVersion: 1, TaskID: taskID, Status: "blocked", Checks: []model.RecoveryCheck{}}
	st, err := c.st.Read()
	if err != nil {
		return out, err
	}
	t := findTask(st, taskID)
	if t == nil {
		return out, store.ErrNotFound
	}
	check := func(name, status, reason string) {
		out.Checks = append(out.Checks, model.RecoveryCheck{Name: name, Status: status, Reason: reason})
	}
	if t.State != model.TaskUnknown {
		check("task", "blocked", "task_not_unknown")
		return out, nil
	}
	var run *model.Run
	for i := len(st.Runs) - 1; i >= 0; i-- {
		if st.Runs[i].TaskID == taskID && st.Runs[i].State == model.RunUnknown {
			run = &st.Runs[i]
			break
		}
	}
	if run == nil {
		check("completion", "unknown", "interrupted_run_missing")
		return out, nil
	}
	out.RunID = run.ID
	v, err := c.st.Completion(run.ID)
	if errors.Is(err, store.ErrNotFound) {
		check("completion", "unknown", "completion_evidence_missing")
		check("process", "unknown", "absence_is_not_completion_proof")
		return out, nil
	}
	if err != nil {
		check("completion", "blocked", "completion_evidence_invalid")
		return out, nil
	}
	if v.State != "pending" {
		check("completion", "blocked", "completion_already_consumed")
		return out, nil
	}
	check("completion", "pass", "")
	e := v.Evidence
	if run.PID != nil {
		if run.Host != c.host || run.ProcessStartedAt == "" || e.ProcessGroupID != *run.PID {
			check("process", "unknown", "process_host_or_identity_unverifiable")
			return out, nil
		}
		if !errors.Is(syscall.Kill(*run.PID, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-e.ProcessGroupID, 0), syscall.ESRCH) {
			check("process", "blocked", "process_or_group_may_be_alive")
			return out, nil
		}
	} else if e.ProcessGroupID != 0 {
		check("process", "unknown", "process_identity_missing")
		return out, nil
	}
	check("process", "pass", "")
	if _, why := frozenOK(st, *t); why != "" {
		check("contract", "blocked", "context_changed")
		return out, nil
	}
	for role := range t.ProfileIDs {
		if _, why := c.verifiedProfile(st, model.Settings{}, *t, role); why != "" {
			check("contract", "blocked", "profile_changed")
			return out, nil
		}
	}
	check("contract", "pass", "")
	f := inspect(t.Worktree)
	if f.Err != nil || !f.Exists {
		check("worktree", "unknown", "worktree_unverifiable")
		return out, nil
	}
	if !f.Clean || f.Branch != deref(t.Branch) || f.Head != e.AfterSession.LastSHA {
		check("worktree", "blocked", "worktree_or_candidate_changed")
		return out, nil
	}
	if _, err := checkRepositoryPair(t.Repository, t.Worktree); err != nil {
		check("worktree", "blocked", "linked_worktree_unverifiable")
		return out, nil
	}
	check("worktree", "pass", "")
	prof, why := c.verifiedProfile(st, model.Settings{}, *t, e.AfterSession.Role)
	x, ok := c.reg[execName(prof)].(executor.StatefulExecutor)
	if why != "" || !ok {
		check("session", "unknown", "executor_cannot_verify_history")
		return out, nil
	}
	snap, err := x.InspectSession(c.sessionBinding(e.AfterSession, t.Worktree))
	if err != nil || snap.Digest != e.AfterSession.FileDigest || snap.ProviderID != e.AfterSession.ProviderID {
		check("session", "blocked", "session_history_changed")
		return out, nil
	}
	check("session", "pass", "")
	p, err := c.st.ProposeRecovery(taskID, run.ID)
	if err != nil {
		check("accounting_and_state", "blocked", "request_accounting_or_state_unresolved")
		return out, nil
	}
	check("accounting_and_state", "pass", "")
	out.Status = "verified"
	out.Proposal = &p
	return out, nil
}
func recoveryReceipt(d model.RecoveryDecision) model.RecoveryReceipt {
	return model.RecoveryReceipt{RequestID: d.RequestID, TaskID: d.Proposal.TaskID, RunID: d.Proposal.RunID, CreatedAt: d.CreatedAt}
}
func (c *Core) ApplyRecovery(in model.RecoveryInput) (model.RecoveryReceipt, error) {
	if !model.ValidRecoveryInput(in) {
		return model.RecoveryReceipt{}, invalid("explicit recovery authorization required")
	}
	old, err := c.st.RecoveryDecision(in.RequestID)
	if err == nil {
		if !reflect.DeepEqual(old.RecoveryInput, in) {
			return model.RecoveryReceipt{}, invalid("request ID belongs to another recovery")
		}
		return recoveryReceipt(old), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.RecoveryReceipt{}, err
	}
	v, err := c.InspectRecovery(in.Proposal.TaskID)
	if err != nil {
		return model.RecoveryReceipt{}, err
	}
	if v.Status != "verified" || v.Proposal == nil || !reflect.DeepEqual(*v.Proposal, in.Proposal) {
		return model.RecoveryReceipt{}, invalid("recovery proposal is blocked or stale")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return model.RecoveryReceipt{}, ErrLeaseLost
	}
	d, err := c.st.ApplyRecoveryOwned(c.token, in)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return recoveryReceipt(d), err
}
func (c *Core) RecoveryDecision(id string) (model.RecoveryReceipt, error) {
	d, err := c.st.RecoveryDecision(id)
	if err != nil {
		return model.RecoveryReceipt{}, err
	}
	return recoveryReceipt(d), nil
}
func ParseRecoveryProposal(raw []byte) (model.RecoveryProposal, error) {
	var p model.RecoveryProposal
	if err := decodeStrict(raw, &p, 16384); err != nil {
		return p, err
	}
	if !model.ValidRecoveryProposal(p) {
		return p, invalid("invalid recovery proposal")
	}
	return p, nil
}
