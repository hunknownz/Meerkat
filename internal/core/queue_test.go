package core

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

func dispatch(t *testing.T, c *Core, ids ...string) model.DispatchReceipt {
	t.Helper()
	r, err := c.Dispatch(model.DispatchRequest{RequestID: newUUID(), TaskIDs: ids})
	if err != nil || !r.Accepted || r.Operation.ID == "" {
		t.Fatalf("dispatch: %+v %v", r, err)
	}
	return r
}

func settled(t *testing.T, c *Core, id string) model.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for {
		o, err := c.WaitOperation(ctx, id, 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if model.OperationSettled(o.State) {
			return o
		}
	}
}

func waitActive(t *testing.T, e *env, n int32) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if e.fx.active.Load() == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("active %d want %d", e.fx.active.Load(), n)
}

func TestAsyncDispatchGlobalConcurrencyAndReceipts(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	a := e.prepare(e.worktree("qa"), nil)
	b := e.prepare(e.worktree("qb"), nil)
	c := e.prepare(e.worktree("qc"), nil)
	ra := dispatch(t, e.c, a.ID)
	rb := dispatch(t, e.c, b.ID)
	rc := dispatch(t, e.c, c.ID)
	waitActive(t, e, 2)
	op, err := e.c.Operation(rc.Operation.ID)
	if err != nil || op.State != model.OperationQueued || op.StartedAt != nil {
		t.Fatalf("queued: %+v %v", op, err)
	}
	dup, err := e.c.Dispatch(model.DispatchRequest{RequestID: ra.Operation.RequestID, TaskIDs: []string{a.ID}})
	if err != nil || !dup.Duplicate || dup.Operation.ID != ra.Operation.ID {
		t.Fatalf("duplicate %+v %v", dup, err)
	}
	if _, err := e.c.Dispatch(model.DispatchRequest{RequestID: ra.Operation.RequestID, TaskIDs: []string{b.ID}}); err == nil {
		t.Fatal("requestId input conflict accepted")
	}
	if _, err := e.c.Dispatch(model.DispatchRequest{RequestID: newUUID(), TaskIDs: []string{a.ID}}); err == nil {
		t.Fatal("same task dispatched twice")
	}
	recovered, err := e.c.OperationByRequest(ra.Operation.RequestID)
	if err != nil || recovered.ID != ra.Operation.ID {
		t.Fatal("lost reply recovery", err)
	}
	// Canceling a wait must leave both owned executions and the queue intact.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.c.WaitOperation(ctx, ra.Operation.ID, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.fx.active.Load() != 2 {
		t.Fatal("wait cancellation stopped a task")
	}
	close(e.fx.gate)
	for _, r := range []model.DispatchReceipt{ra, rb, rc} {
		o := settled(t, e.c, r.Operation.ID)
		if o.State != model.OperationCompleted || o.Tasks[0].TaskState != model.TaskDelivered || o.Tasks[0].CandidateSHA == nil {
			t.Fatalf("outcome %+v", o)
		}
		if o.RequestDigest != "" || o.Tasks[0].ContractDigest != "" {
			t.Fatal("private digests exposed")
		}
	}
	if e.fx.maxActive.Load() != 2 {
		t.Fatal("separate dispatches exceeded or missed global concurrency")
	}
}

func TestAsyncSameWorktreeQueuesBehindOwnedTask(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	wt := e.worktree("serial")
	a, b := e.prepare(wt, nil), e.prepare(wt, nil)
	ra := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	rb := dispatch(t, e.c, b.ID)
	o, err := e.c.WaitOperation(context.Background(), rb.Operation.ID, 100*time.Millisecond)
	if err != nil || o.State != model.OperationQueued || o.Tasks[0].StartedAt != nil {
		t.Fatal(o, err)
	}
	close(e.fx.gate)
	if o := settled(t, e.c, ra.Operation.ID); o.Tasks[0].TaskState != model.TaskDelivered {
		t.Fatal(o)
	}
	o = settled(t, e.c, rb.Operation.ID)
	if o.State != model.OperationCompleted || o.Tasks[0].TaskState != model.TaskFailed || deref(o.Tasks[0].StateReason) != "baseline_changed_requires_prepare" {
		t.Fatal(o)
	}
	if e.fx.maxActive.Load() != 1 {
		t.Fatal("same worktree ran concurrently")
	}
}

func TestAsyncPartialQueuedFailureDoesNotStallOperation(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	one := 1
	if _, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &one}); err != nil {
		t.Fatal(err)
	}
	a := e.prepare(e.worktree("partial-active"), nil)
	b := e.prepare(e.worktree("partial-rejected"), nil)
	c := e.prepare(e.worktree("partial-valid"), nil)
	ra := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	r := dispatch(t, e.c, b.ID, c.ID)
	if err := e.c.update(func(s *model.State) error {
		findTask(s, b.ID).Goal = "changed after dispatch"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	partial := false
	for time.Now().Before(deadline) {
		o, err := e.c.Operation(r.Operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if o.Tasks[0].State == model.MemberCompleted {
			if o.State != model.OperationRunning || o.StartedAt == nil || o.Tasks[0].TaskState != model.TaskFailed || o.Tasks[1].State != model.MemberQueued {
				t.Fatal(o)
			}
			partial = true
			break
		}
		time.Sleep(e.c.opts.Poll)
	}
	if !partial {
		t.Fatal("preflight failure did not persist while another member was queued")
	}
	close(e.fx.gate)
	settled(t, e.c, ra.Operation.ID)
	o := settled(t, e.c, r.Operation.ID)
	if o.State != model.OperationCompleted || o.Tasks[0].TaskState != model.TaskFailed || o.Tasks[1].TaskState != model.TaskDelivered {
		t.Fatal(o)
	}
}

func TestAsyncFrozenContractAndDefaultProfiles(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	one := 1
	if _, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &one}); err != nil {
		t.Fatal(err)
	}
	a := e.prepare(e.worktree("fa"), nil)
	b := e.prepare(e.worktree("fb"), nil)
	c := e.prepare(e.worktree("fc"), nil)
	other := e.prepare(e.worktree("fd"), nil)
	ra := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	rb, rc := dispatch(t, e.c, b.ID), dispatch(t, e.c, c.ID)
	// A future default must not replace b's frozen developer profile.
	if _, err := e.c.Settings(model.SettingsPatch{DefaultProfiles: map[string]map[string]string{"demo": {"developer": other.ProfileIDs["developer"]}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.c.update(func(s *model.State) error { findTask(s, c.ID).Goal = "changed outside frozen contract"; return nil }); err != nil {
		t.Fatal(err)
	}
	close(e.fx.gate)
	settled(t, e.c, ra.Operation.ID)
	if o := settled(t, e.c, rb.Operation.ID); o.Tasks[0].TaskState != model.TaskDelivered {
		t.Fatalf("recovered state=%s reason=%s", o.Tasks[0].TaskState, deref(o.Tasks[0].StateReason))
	}
	if o := settled(t, e.c, rc.Operation.ID); o.Tasks[0].TaskState != model.TaskFailed || deref(o.Tasks[0].StateReason) != "dispatch_contract_changed" {
		t.Fatal(o)
	}
	e.fx.mu.Lock()
	defer e.fx.mu.Unlock()
	for _, req := range e.fx.reqs {
		if req.Worktree == c.Worktree {
			t.Fatal("modified frozen contract executed")
		}
		if req.Worktree == b.Worktree && req.Role == "developer" && req.Profile.ID != b.ProfileIDs["developer"] {
			t.Fatal("frozen profile replaced")
		}
	}
}

func TestAsyncGracefulRestartPreservesNeverStartedQueue(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	one := 1
	e.c.Settings(model.SettingsPatch{MaxConcurrency: &one})
	a := e.prepare(e.worktree("restart-a"), nil)
	b := e.prepare(e.worktree("restart-b"), nil)
	ra := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	rb := dispatch(t, e.c, b.ID)
	if err := e.c.Close(); err != nil {
		t.Fatal(err)
	}
	if o, err := e.st.Operation(ra.Operation.ID); err != nil || o.State != model.OperationCompleted || o.Tasks[0].TaskState != model.TaskStopped {
		t.Fatal(o, err)
	}
	if o, err := e.st.Operation(rb.Operation.ID); err != nil || o.State != model.OperationQueued || o.StartedAt != nil {
		t.Fatal(o, err)
	}
	close(e.fx.gate)
	e.c = e.newCore()
	if o := settled(t, e.c, rb.Operation.ID); o.Tasks[0].TaskState != model.TaskDelivered {
		t.Fatalf("recovered state=%s reason=%s", o.Tasks[0].TaskState, deref(o.Tasks[0].StateReason))
	}
	dup, err := e.c.Dispatch(model.DispatchRequest{RequestID: rb.Operation.RequestID, TaskIDs: []string{b.ID}})
	if err != nil || !dup.Duplicate || dup.Operation.ID != rb.Operation.ID {
		t.Fatal("restart lost receipt", err)
	}
}

func TestAsyncStartedRestartUnknownNeverReplays(t *testing.T) {
	e := setup(t)
	a := e.prepare(e.worktree("uncertain"), nil)
	r := dispatch(t, e.c, a.ID)
	settled(t, e.c, r.Operation.ID)
	if err := e.c.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after durable start, before a process/operation outcome.
	l, err := e.st.AcquireLease()
	if err != nil {
		t.Fatal(err)
	}
	err = e.st.UpdateOperationOwned(l.Token, r.Operation.ID, func(o *model.Operation, s *model.State) error {
		o.State, o.EndedAt = model.OperationRunning, nil
		o.Tasks[0].State, o.Tasks[0].EndedAt = model.MemberRunning, nil
		task := findTask(s, a.ID)
		task.State = model.TaskImplementing
		pid := os.Getpid()
		s.Runs = append(s.Runs, model.Run{ID: newUUID(), TaskID: a.ID, Role: "developer", State: model.RunStarting, PID: &pid, Host: e.c.host, StartedAt: now(), UpdatedAt: now()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.st.ReleaseLease(l.Token)
	e.fx.mu.Lock()
	before := len(e.fx.roles)
	e.fx.mu.Unlock()
	e.c = e.newCore()
	o, err := e.c.Operation(r.Operation.ID)
	if err != nil || o.State != model.OperationUnknown || o.Tasks[0].TaskState != model.TaskUnknown {
		t.Fatal(o, err)
	}
	time.Sleep(3 * e.c.opts.Poll)
	e.fx.mu.Lock()
	after := len(e.fx.roles)
	e.fx.mu.Unlock()
	if after != before {
		t.Fatal("uncertain operation replayed")
	}
	if _, err := e.c.Dispatch(model.DispatchRequest{RequestID: newUUID(), TaskIDs: []string{a.ID}, Resume: true, Acknowledge: true}); err == nil || !strings.Contains(err.Error(), "alive") {
		t.Fatal("live old PID was ignored", err)
	}
	if !slices.ContainsFunc(e.state().Runs, func(run model.Run) bool { return run.TaskID == a.ID && run.State == model.RunUnknown }) {
		t.Fatal("unknown run lost")
	}
	if _, err := e.st.OperationByRequest(r.Operation.RequestID); errors.Is(err, store.ErrNotFound) {
		t.Fatal("unknown receipt lost")
	}
}

func TestAsyncConcurrentDuplicateAndInputOrder(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	wt := e.worktree("ordered")
	a, b := e.prepare(wt, nil), e.prepare(wt, nil)
	req := model.DispatchRequest{RequestID: newUUID(), TaskIDs: []string{a.ID, b.ID}}
	var wg sync.WaitGroup
	type reply struct {
		receipt model.DispatchReceipt
		err     error
	}
	replies := make(chan reply, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := e.c.Dispatch(req); replies <- reply{r, err} }()
	}
	wg.Wait()
	close(replies)
	id := ""
	for r := range replies {
		if r.err != nil || !r.receipt.Accepted {
			t.Fatal(r.err, r.receipt)
		}
		if id == "" {
			id = r.receipt.Operation.ID
		}
		if r.receipt.Operation.ID != id {
			t.Fatal("duplicate produced a new operation")
		}
	}
	if _, err := e.c.Dispatch(model.DispatchRequest{RequestID: req.RequestID, TaskIDs: []string{b.ID, a.ID}}); err == nil {
		t.Fatal("changed scheduling order accepted under original requestId")
	}
	close(e.fx.gate)
	o := settled(t, e.c, id)
	if o.Tasks[0].TaskID != a.ID || o.Tasks[0].TaskState != model.TaskDelivered || o.Tasks[1].TaskState != model.TaskFailed {
		t.Fatal("original scheduling order lost", o)
	}
}

func TestAsyncFixRoundSettingFrozenAtDispatch(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	e.fx.verdicts = []string{"changes_requested", "pass"}
	a := e.prepare(e.worktree("fix-frozen"), nil)
	r := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	zero := 0
	if _, err := e.c.Settings(model.SettingsPatch{MaxFixRounds: &zero}); err != nil {
		t.Fatal(err)
	}
	close(e.fx.gate)
	o := settled(t, e.c, r.Operation.ID)
	if o.Tasks[0].TaskState != model.TaskDelivered || o.FrozenFixRounds != 2 {
		t.Fatal("running fix policy was rewritten", o)
	}
	e.fx.mu.Lock()
	defer e.fx.mu.Unlock()
	n := 0
	for _, role := range e.fx.roles {
		if role == "developer" {
			n++
		}
	}
	if n != 2 {
		t.Fatal("frozen repair allowance not used", n)
	}
}
