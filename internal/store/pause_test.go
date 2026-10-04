package store

import (
	"encoding/json"
	"errors"
	"github.com/hunknownz/Meerkat/internal/model"
	"strings"
	"testing"
)

func TestPauseRejectsPendingAndNewDirectionsWithDurableReceipts(t *testing.T) {
	s, l, _, in := controlFixture(t)
	in.Kind = "follow_up"
	in.Message = "PRIVATE_FOLLOW_UP"
	first, err := s.RequestFollowUpOwned(l.Token, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestFollowUpOwned(l.Token, in); err != nil {
		t.Fatal(err)
	}
	pause := in
	pause.Kind = "pause"
	pause.Message = ""
	pause.RequestID = "90000000-0000-4000-8000-000000000009"
	if _, err = s.RequestPauseOwned(l.Token, pause); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestPauseOwned(l.Token, pause); err != nil {
		t.Fatal(err)
	}
	rc, err := s.ControlReceipt(first.Input.RequestID)
	if err != nil || rc.State != model.ControlRejected || rc.Reason == nil || *rc.Reason != "pause_requested" {
		t.Fatal(rc, err)
	}
	in.RequestID = "90000000-0000-4000-8000-000000000008"
	if _, err = s.RequestFollowUpOwned(l.Token, in); !errors.Is(err, ErrConflict) {
		t.Fatal("text accepted after pause", err)
	}
	if err = s.BeginControlOwned(l.Token, pause.RequestID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishControlOwned(l.Token, pause.RequestID, model.ControlAcknowledged, "queued", ""); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Read()
	receipts, err := s.ControlSummaries(first.TaskID, st)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(receipts)
	if strings.Contains(string(raw), first.Input.Message) {
		t.Fatal("private text leaked")
	}
}

func TestV10MigrationPreservesPayloadAndInsertionOrder(t *testing.T) {
	s, l, _, in := controlFixture(t)
	_, err := s.RequestWrapUpOwned(l.Token, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Kind = "instruction"
	in.Message = "existing direction"
	in.RequestID = "90000000-0000-4000-8000-000000000008"
	_, err = s.RequestInstructionOwned(l.Token, in)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := controls(s.rdb, "")
	if _, err = s.db.Exec(`DROP INDEX run_controls_task; DROP INDEX run_controls_wrap_up; DROP INDEX run_controls_pause;
 ALTER TABLE run_controls RENAME TO current_controls;
 CREATE TABLE run_controls(request_id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(id),task_id TEXT NOT NULL REFERENCES tasks(id),session_id TEXT NOT NULL REFERENCES execution_sessions(id),kind TEXT NOT NULL CHECK(kind IN ('wrap_up','instruction')),state TEXT NOT NULL,payload TEXT NOT NULL);
 INSERT INTO run_controls SELECT * FROM current_controls ORDER BY rowid; DROP TABLE current_controls;
 CREATE INDEX run_controls_task ON run_controls(task_id); CREATE UNIQUE INDEX run_controls_wrap_up ON run_controls(run_id) WHERE kind='wrap_up'; PRAGMA user_version=10;`); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	s.Close()
	next, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	after, err := controls(next.rdb, "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("historical payload/order changed")
	}
	var version int
	next.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 12 {
		t.Fatal(version)
	}
}
