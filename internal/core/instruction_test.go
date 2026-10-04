package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
)

func TestOwnedInstructionsAdvanceQueueAndDeduplicate(t *testing.T) {
	e := setup(t)
	e.c.Close()
	f := &controlledFake{statefulFake: &statefulFake{fakeExec: e.fx}, ready: make(chan executor.Request, 1), consume: make(chan struct{}), finish: make(chan struct{}), controlCount: 3}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: 10 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	task := e.prepare(e.worktree("instructions"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() { res, _ := c.Execute(ctx, []string{task.ID}, false, false); done <- res }()
	var req executor.Request
	select {
	case req = <-f.ready:
	case <-ctx.Done():
		t.Fatal("run not started")
	}
	in := model.WrapUpInput{Kind: "instruction", RunID: req.RunID, SessionID: req.Session.ID, RequestID: newUUID(), Message: "PRIVATE_HUMAN_DIRECTION", AuthorizationRef: "Human UI action", Apply: true}
	bad := in
	bad.SessionID = newUUID()
	if _, err = c.RequestInstruction(bad); err == nil {
		t.Fatal("wrong session")
	}
	if _, err = c.RequestInstruction(in); err != nil {
		t.Fatal(err)
	}
	if _, err = c.RequestInstruction(in); err != nil {
		t.Fatal("duplicate", err)
	}
	second := in
	second.RequestID = newUUID()
	second.Message = "Next bounded direction"
	if _, err = c.RequestInstruction(second); err != nil {
		t.Fatal(err)
	}
	wrap := in
	wrap.Kind = ""
	wrap.Message = ""
	wrap.RequestID = newUUID()
	if _, err = c.RequestWrapUp(wrap); err != nil {
		t.Fatal(err)
	}
	close(f.consume)
	deadline := time.Now().Add(3 * time.Second)
	for f.effects.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.effects.Load() != 3 {
		t.Fatal("queue did not advance", f.effects.Load())
	}
	rc, err := c.ControlReceipt(second.RequestID)
	if err != nil || rc.State != model.ControlAcknowledged || rc.Outcome != nil {
		t.Fatal(rc, err)
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(snap)
	if strings.Contains(string(b), in.Message) || strings.Contains(string(b), in.AuthorizationRef) {
		t.Fatal("private direction exposed")
	}
	close(f.finish)
	res := <-done
	if len(res.Tasks) != 1 || res.Tasks[0].State != model.TaskDelivered {
		t.Fatal(res)
	}
	if _, err = c.RequestInstruction(in); err != nil {
		t.Fatal("ended receipt lost", err)
	}
	in.RequestID = newUUID()
	if _, err = c.RequestInstruction(in); err == nil {
		t.Fatal("new write to ended run")
	}
}
