package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func controlFixture(t *testing.T) (*Store, model.ControllerLease, model.Session, model.WrapUpInput) {
	t.Helper()
	s, l, ss, rid := sessionFixture(t)
	st, e := s.Read()
	if e != nil {
		t.Fatal(e)
	}
	ss.ContractDigest = model.FrozenTaskDigest(st, *taskByID(st, ss.TaskID))
	if e := s.StartSessionRunOwned(l.Token, ss, rid, true, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: rid, TaskID: ss.TaskID, Role: ss.Role, ProfileID: ss.ProfileID, State: model.RunRunning, UpdatedAt: now()})
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return s, l, ss, model.WrapUpInput{RunID: rid, SessionID: ss.ID, RequestID: "90000000-0000-4000-8000-000000000001", AuthorizationRef: "EXPLICIT_PRIVATE_AUTHORIZATION", Apply: true}
}

func TestControlDurabilityIdempotencyAndNamespace(t *testing.T) {
	s, l, ss, in := controlFixture(t)
	var wg sync.WaitGroup
	errCh := make(chan error, 12)
	for i := 0; i < cap(errCh); i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.RequestWrapUpOwned(l.Token, in); errCh <- e }()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	all, e := s.PendingControls()
	if e != nil || len(all) != 1 {
		t.Fatal(all, e)
	}
	if _, e = s.RequestStop(in.RunID, in.RequestID); !errors.Is(e, ErrConflict) {
		t.Fatal("control ID became a stop", e)
	}
	changed := in
	changed.AuthorizationRef = "changed"
	if _, e = s.RequestWrapUpOwned(l.Token, changed); !errors.Is(e, ErrConflict) {
		t.Fatal("changed authority accepted", e)
	}
	changed = in
	changed.RequestID = "90000000-0000-4000-8000-000000000002"
	if _, e = s.RequestWrapUpOwned(l.Token, changed); !errors.Is(e, ErrConflict) {
		t.Fatal("second wrap-up accepted", e)
	}
	if e = s.BeginControlOwned(l.Token, in.RequestID); e != nil {
		t.Fatal(e)
	}
	if e = s.BeginControlOwned(l.Token, in.RequestID); !errors.Is(e, ErrConflict) {
		t.Fatal("send repeated", e)
	}
	if e = s.FinishControlOwned(l.Token, in.RequestID, model.ControlAcknowledged, "queued", ""); e != nil {
		t.Fatal(e)
	}
	before, e := s.ControlReceipt(in.RequestID)
	if e != nil || !model.ValidControlReceipt(before) || before.Outcome != nil || before.RunState != model.RunRunning {
		t.Fatal(before, e)
	}
	ss.UpdatedAt = now()
	if e = s.FinishSessionRunOwned(l.Token, ss, in.RunID, func(st *model.State) error { controlRun(st, in.RunID).State = model.RunSucceeded; return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RequestWrapUpOwned(l.Token, in); e != nil {
		t.Fatal("ended duplicate lost receipt", e)
	}
	got, e := s.ControlReceipt(in.RequestID)
	if e != nil || got.State != model.ControlAcknowledged || got.Outcome == nil || *got.Outcome != model.RunSucceeded {
		t.Fatal(got, e)
	}
	stopID := "90000000-0000-4000-8000-000000000003"
	if _, e = s.RequestStop(in.RunID, stopID); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishStopOwned(l.Token, stopID, model.RunSucceeded); e != nil {
		t.Fatal(e)
	}
	stop, e := s.ControlReceipt(stopID)
	if e != nil || stop.Kind != "stop" || stop.State != "processed" || !model.ValidControlReceipt(stop) {
		t.Fatal(stop, e)
	}
	pending, _ := s.StopRequests()
	if len(pending) != 0 {
		t.Fatal("query queued a stop")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), in.AuthorizationRef) || strings.Contains(string(raw), ss.ProfileDigest) {
		t.Fatal("private authority in receipt")
	}
	backup := filepath.Join(t.TempDir(), "controls.db")
	if e = s.Backup(backup); e != nil {
		t.Fatal(e)
	}
	dst := filepath.Join(t.TempDir(), "restore")
	if e = Restore(backup, dst); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(dst)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	v, e := restored.ControlReceipt(in.RequestID)
	if e != nil || !reflect.DeepEqual(got, v) {
		t.Fatal("receipt changed on restore", v, e)
	}
	db, e := sql.Open("sqlite", dsn(backup, dsnBackupFile))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("UPDATE run_controls SET state='sending'")
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateBackup(backup); !errors.Is(e, ErrBadBackup) {
		t.Fatal("corrupt receipt accepted", e)
	}
}

func TestControlInterruptedDoesNotReplayAndBlocksRecovery(t *testing.T) {
	for _, sending := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "sending"}[sending], func(t *testing.T) {
			s, l, _, in := controlFixture(t)
			if _, e := s.RequestWrapUpOwned(l.Token, in); e != nil {
				t.Fatal(e)
			}
			if sending {
				if e := s.BeginControlOwned(l.Token, in.RequestID); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.ReconcileControlsOwned(l.Token); e != nil {
				t.Fatal(e)
			}
			v, e := s.ControlReceipt(in.RequestID)
			if e != nil || v.State != model.ControlUnknown || v.Reason == nil || *v.Reason != "controller_interrupted" {
				t.Fatal(v, e)
			}
			if all, e := s.PendingControls(); e != nil || len(all) != 0 {
				t.Fatal("interrupted command replayable", all, e)
			}
			if e = recoveryBudget(s.rdb, v.TaskID); e == nil {
				t.Fatal("uncertain control allowed recovery")
			}
			if e = s.BeginControlOwned(l.Token, in.RequestID); e == nil {
				t.Fatal("unknown resent")
			}
		})
	}
}

func TestControlWrongBindingStopCollisionAndUnsentExit(t *testing.T) {
	s, l, _, in := controlFixture(t)
	bad := in
	bad.SessionID = "90000000-0000-4000-8000-000000000008"
	if _, e := s.RequestWrapUpOwned(l.Token, bad); e == nil {
		t.Fatal("wrong session")
	}
	if _, e := s.RequestStop(in.RunID, in.RequestID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RequestWrapUpOwned(l.Token, in); !errors.Is(e, ErrConflict) {
		t.Fatal("stop ID became wrap-up", e)
	}
	in.RequestID = "90000000-0000-4000-8000-000000000009"
	if _, e := s.RequestWrapUpOwned(l.Token, in); e != nil {
		t.Fatal(e)
	}
	if e := s.CloseControlsOwned(l.Token, in.RunID); e != nil {
		t.Fatal(e)
	}
	v, e := s.ControlReceipt(in.RequestID)
	if e != nil || v.State != model.ControlRejected || v.Reason == nil || *v.Reason != "run_ended_before_send" {
		t.Fatal(v, e)
	}
}
