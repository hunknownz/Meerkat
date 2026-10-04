package core

import (
	"context"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
)

type humanPauseFake struct {
	*checkpointFake
	ready chan executor.Request
}

func (f *humanPauseFake) Capabilities() executor.Capabilities {
	return executor.Capabilities{PersistentSessions: true, GracefulWrapUp: true, GracefulPause: true, QueuedFollowUp: true}
}
func (f *humanPauseFake) Execute(ctx context.Context, req executor.Request, event func(model.RunEvent), start func(executor.Process)) (executor.Result, error) {
	if !f.paused {
		f.ready <- req
		select {
		case v := <-req.Controls.Messages:
			if v.Kind != "pause" {
				return executor.Result{}, invalid("unexpected control")
			}
			if err := req.Controls.Authority.Begin(v.ID); err != nil {
				return executor.Result{}, err
			}
			if err := req.Controls.Authority.Finish(v.ID, model.ControlAcknowledged, "queued", ""); err != nil {
				return executor.Result{}, err
			}
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
		r, err := f.checkpointFake.Execute(ctx, req, event, start)
		if err != nil {
			r.Category = executor.CatPauseRequested
			return r, &executor.Error{Category: executor.CatPauseRequested}
		}
		return r, err
	}
	return f.checkpointFake.Execute(ctx, req, event, start)
}
func TestHumanPauseCheckpointResumesSameSessionAndChargesPastUsage(t *testing.T) {
	e, base := checkpointEnv(t)
	e.c.Close()
	f := &humanPauseFake{checkpointFake: base, ready: make(chan executor.Request, 1)}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	task := e.prepare(e.worktree("human-pause"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() { res, _ := c.Execute(ctx, []string{task.ID}, false, false); done <- res }()
	var req executor.Request
	select {
	case req = <-f.ready:
	case <-ctx.Done():
		t.Fatal("not started")
	}
	in := model.WrapUpInput{Kind: "pause", RunID: req.RunID, SessionID: req.Session.ID, RequestID: newUUID(), AuthorizationRef: "User requested pause", Apply: true}
	if _, err = c.RequestPause(in); err != nil {
		t.Fatal(err)
	}
	if _, err = c.RequestPause(in); err != nil {
		t.Fatal("duplicate", err)
	}
	res := <-done
	if len(res.Tasks) != 1 || res.Tasks[0].State != model.TaskPaused || deref(res.Tasks[0].StateReason) != "pause_requested" {
		t.Fatal(res)
	}
	cp, err := e.st.CheckpointsForTask(task.ID)
	if err != nil || len(cp) != 1 || cp[0].SessionID != req.Session.ID {
		t.Fatal(cp, err)
	}
	rc, err := c.ControlReceipt(in.RequestID)
	if err != nil || rc.State != model.ControlAcknowledged || rc.Outcome == nil || *rc.Outcome != model.RunStopped {
		t.Fatal(rc, err)
	}
	res, err = c.Execute(ctx, []string{task.ID}, true, false)
	if err != nil || res.Tasks[0].State != model.TaskDelivered {
		t.Fatal(res, err)
	}
	after, _ := e.st.CheckpointsForTask(task.ID)
	if after[0].State != "consumed" {
		t.Fatal(after)
	}
	ss, _ := e.st.SessionForRun(*after[0].ResumedRunID)
	if ss.ID != req.Session.ID {
		t.Fatal("session replaced")
	}
	tokens, _ := budgetUse(e.state(), task.ID)
	if tokens != 500 {
		t.Fatal("past usage lost", tokens)
	}
}
