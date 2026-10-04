package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func TestInstructionsShareRunDeduplicateAndKeepTextPrivate(t *testing.T) {
	s, l, _, in := controlFixture(t)
	if _, e := s.RequestWrapUpOwned(l.Token, in); e != nil {
		t.Fatal(e)
	}
	in.Kind = "instruction"
	in.Message = "PRIVATE_HUMAN_DIRECTION"
	in.RequestID = "90000000-0000-4000-8000-000000000002"
	first, e := s.RequestInstructionOwned(l.Token, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RequestInstructionOwned(l.Token, in); e != nil {
		t.Fatal(e)
	}
	bad := in
	bad.Message = "different"
	if _, e = s.RequestInstructionOwned(l.Token, bad); !errors.Is(e, ErrConflict) {
		t.Fatal("changed request accepted", e)
	}
	in.RequestID = "90000000-0000-4000-8000-000000000003"
	if _, e = s.RequestInstructionOwned(l.Token, in); e != nil {
		t.Fatal("second instruction refused", e)
	}
	pending, e := s.PendingControls()
	if e != nil || len(pending) != 3 {
		t.Fatal(pending, e)
	}
	if e = s.BeginControlOwned(l.Token, first.Input.RequestID); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishControlOwned(l.Token, first.Input.RequestID, model.ControlAcknowledged, "queued", ""); e != nil {
		t.Fatal(e)
	}
	st, _ := s.Read()
	receipts, e := s.ControlSummaries(first.TaskID, st)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(receipts)
	if strings.Contains(string(b), first.Input.Message) || strings.Contains(string(b), first.Input.AuthorizationRef) {
		t.Fatal("private control text leaked")
	}
	if e = s.ReconcileControlsOwned(l.Token); e != nil {
		t.Fatal(e)
	}
	if pending, e = s.PendingControls(); e != nil || len(pending) != 0 {
		t.Fatal("restart replay", pending, e)
	}
	if rc, e := s.ControlReceipt(in.RequestID); e != nil || rc.State != model.ControlUnknown {
		t.Fatal(rc, e)
	}
}

func TestV9MigrationPreservesHistoricalControlPayload(t *testing.T) {
	s, l, _, in := controlFixture(t)
	before, e := s.RequestWrapUpOwned(l.Token, in)
	if e != nil {
		t.Fatal(e)
	}
	// Downgrade only the table shape in this isolated database, then exercise the
	// actual Open migration. The legacy serialized input omits kind and message.
	_, e = s.db.Exec(`DROP INDEX run_controls_task; DROP INDEX run_controls_wrap_up; DROP INDEX run_controls_pause;
ALTER TABLE run_controls RENAME TO controls_new;
CREATE TABLE run_controls(request_id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(id),task_id TEXT NOT NULL REFERENCES tasks(id),session_id TEXT NOT NULL REFERENCES execution_sessions(id),kind TEXT NOT NULL CHECK(kind='wrap_up'),state TEXT NOT NULL,payload TEXT NOT NULL,UNIQUE(run_id,kind));
INSERT INTO run_controls SELECT * FROM controls_new; DROP TABLE controls_new;
CREATE INDEX run_controls_task ON run_controls(task_id); PRAGMA user_version=9;`)
	if e != nil {
		t.Fatal(e)
	}
	dir := s.dir
	s.Close()
	next, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	after, e := next.Control(in.RequestID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("legacy payload or digest changed", after, e)
	}
	var version int
	next.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 12 {
		t.Fatal(version)
	}
}
