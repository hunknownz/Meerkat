package core

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
)

type controlledFake struct {
	*statefulFake
	ready           chan executor.Request
	consume, finish chan struct{}
	effects         atomic.Int32
	controlCount    int
}

func (f *controlledFake) Capabilities() executor.Capabilities {
	return executor.Capabilities{Protocol: "control-fixture", PersistentSessions: true, GracefulWrapUp: true}
}
func (f *controlledFake) Execute(ctx context.Context, r executor.Request, event func(model.RunEvent), start func(executor.Process)) (executor.Result, error) {
	if r.Role == "developer" {
		start(executor.Process{Executor: "fake", Host: "test", StartedAt: time.Now()})
		f.ready <- r
		select {
		case <-f.consume:
		case <-ctx.Done():
		}
		for n := 0; n < max(1, f.controlCount); n++ {
			select {
			case v := <-r.Controls.Messages:
				if e := r.Controls.Authority.Begin(v.ID); e != nil {
					return executor.Result{Category: executor.CatSessionUnknown}, e
				}
				f.effects.Add(1)
				if e := r.Controls.Authority.Finish(v.ID, model.ControlAcknowledged, "queued", ""); e != nil {
					return executor.Result{}, e
				}
			case <-ctx.Done():
			}
		}
		select {
		case <-f.finish:
		case <-ctx.Done():
		}
	}
	return f.statefulFake.Execute(ctx, r, event, start)
}

func TestControlOwnedLaneConcurrentRequestIsOneEffectAndActualDeliveryIsSeparate(t *testing.T) {
	e := setup(t)
	e.c.Close()
	f := &controlledFake{statefulFake: &statefulFake{fakeExec: e.fx}, ready: make(chan executor.Request, 1), consume: make(chan struct{}), finish: make(chan struct{})}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: 10 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	task := e.prepare(e.worktree("controls"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() { r, _ := c.Execute(ctx, []string{task.ID}, false, false); done <- r }()
	var req executor.Request
	select {
	case req = <-f.ready:
	case <-ctx.Done():
		t.Fatal("run did not start")
	}
	in := model.WrapUpInput{RunID: req.RunID, SessionID: req.Session.ID, RequestID: newUUID(), AuthorizationRef: "PRIVATE_CONTROL_AUTH", Apply: true}
	bad := in
	bad.Apply = false
	if _, err = c.RequestWrapUp(bad); err == nil {
		t.Fatal("implicit wrap-up")
	}
	bad = in
	bad.SessionID = newUUID()
	if _, err = c.RequestWrapUp(bad); err == nil {
		t.Fatal("wrong session")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := c.RequestWrapUp(in)
			if e == nil && (v.State != model.ControlAccepted || v.Outcome != nil) {
				t.Error("acceptance claimed outcome", v)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	close(f.consume)
	deadline := time.Now().Add(3 * time.Second)
	var receipt model.ControlReceipt
	for time.Now().Before(deadline) {
		receipt, err = c.ControlReceipt(in.RequestID)
		if err == nil && receipt.State == model.ControlAcknowledged {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || receipt.State != model.ControlAcknowledged || receipt.RunState != model.RunRunning || receipt.Outcome != nil || f.effects.Load() != 1 {
		t.Fatal(receipt, err, f.effects.Load())
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), in.AuthorizationRef) || strings.Contains(string(raw), "controlSession") {
		t.Fatal("private control authority leaked")
	}
	if len(snap.Tasks[0].ControlReceipts) != 1 {
		t.Fatal("receipt missing from task")
	}
	close(f.finish)
	res := <-done
	if len(res.Tasks) != 1 || res.Tasks[0].State != model.TaskDelivered {
		t.Fatal(res)
	}
	receipt, err = c.RequestWrapUp(in)
	if err != nil || receipt.Outcome == nil || *receipt.Outcome != model.RunSucceeded || f.effects.Load() != 1 {
		t.Fatal("ended duplicate changed effect", receipt, err)
	}
	bad = in
	bad.RequestID = newUUID()
	if _, err = c.RequestWrapUp(bad); err == nil {
		t.Fatal("ended run accepted new instruction")
	}
}

func TestControlChangedProfileBeforeSendRefusesProtocolEffect(t *testing.T) {
	e := setup(t)
	e.c.Close()
	f := &controlledFake{statefulFake: &statefulFake{fakeExec: e.fx}, ready: make(chan executor.Request, 1), consume: make(chan struct{}), finish: make(chan struct{})}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: 10 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	task := e.prepare(e.worktree("control-profile"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() { r, _ := c.Execute(ctx, []string{task.ID}, false, false); done <- r }()
	var req executor.Request
	select {
	case req = <-f.ready:
	case <-ctx.Done():
		t.Fatal("not started")
	}
	in := model.WrapUpInput{RunID: req.RunID, SessionID: req.Session.ID, RequestID: newUUID(), AuthorizationRef: "test", Apply: true}
	if _, err = c.RequestWrapUp(in); err != nil {
		t.Fatal(err)
	}
	// Re-read the actual private profile, not merely its cached database value.
	b, err := os.ReadFile(e.profiles["developer"])
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if json.Unmarshal(b, &p) != nil {
		t.Fatal("profile unreadable")
	}
	p["model"] = "changed-model"
	b, _ = json.Marshal(p)
	if err = os.WriteFile(e.profiles["developer"], b, 0o600); err != nil {
		t.Fatal(err)
	}
	close(f.consume)
	close(f.finish)
	res := <-done
	v, err := c.ControlReceipt(in.RequestID)
	if err != nil || v.State != model.ControlRejected || v.Reason == nil || *v.Reason != "contract_changed" || f.effects.Load() != 0 || res.Tasks[0].State == model.TaskDelivered {
		t.Fatal(v, err, res)
	}
}

func TestControlControllerRestartPreservesUnknownAndNeverReplays(t *testing.T) {
	for _, sending := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "sending"}[sending], func(t *testing.T) {
			e := setup(t)
			f := useSessions(t, e)
			task := e.prepare(e.worktree("control-restart"), nil)
			st := e.state()
			var p model.Profile
			for _, v := range st.Profiles {
				if v.ID == task.ProfileIDs["developer"] {
					p = v
				}
			}
			ss, fresh, e2 := e.c.selectSession(st, task, p, "developer", deref(task.BaselineSha))
			if e2 != nil {
				t.Fatal(e2)
			}
			rid := newUUID()
			if e2 = e.c.startRoleRecord(ss, rid, fresh, func(st *model.State) error {
				st.Runs = append(st.Runs, model.Run{ID: rid, TaskID: task.ID, Role: ss.Role, ProfileID: ss.ProfileID, State: model.RunRunning, UpdatedAt: now()})
				findTask(st, task.ID).State = model.TaskImplementing
				return nil
			}); e2 != nil {
				t.Fatal(e2)
			}
			in := model.WrapUpInput{RunID: rid, SessionID: ss.ID, RequestID: newUUID(), AuthorizationRef: "test", Apply: true}
			if _, e2 = e.st.RequestWrapUpOwned(e.c.token, in); e2 != nil {
				t.Fatal(e2)
			}
			if sending {
				if e2 = e.st.BeginControlOwned(e.c.token, in.RequestID); e2 != nil {
					t.Fatal(e2)
				}
			}
			if e2 = e.c.Close(); e2 != nil {
				t.Fatal(e2)
			}
			c, e2 := New(e.st, Registry{"fake": f})
			if e2 != nil {
				t.Fatal(e2)
			}
			e.c = c
			v, e2 := c.ControlReceipt(in.RequestID)
			if e2 != nil || v.State != model.ControlUnknown || v.RunState != model.RunUnknown || v.Outcome == nil || *v.Outcome != model.RunUnknown {
				t.Fatal(v, e2)
			}
			if len(e.fx.reqs) != 0 {
				t.Fatal("restart replayed agent")
			}
			if pending, e2 := e.st.PendingControls(); e2 != nil || len(pending) != 0 {
				t.Fatal("restart queued uncertain instruction")
			}
		})
	}
}
