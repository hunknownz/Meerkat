package store

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

const controlTable = "run_controls"
const migrationV9 = `
CREATE TABLE run_controls(request_id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id),
 task_id TEXT NOT NULL REFERENCES tasks(id), session_id TEXT NOT NULL REFERENCES execution_sessions(id),
 kind TEXT NOT NULL CHECK(kind='wrap_up'), state TEXT NOT NULL, payload TEXT NOT NULL, UNIQUE(run_id,kind));
CREATE INDEX run_controls_task ON run_controls(task_id);
PRAGMA user_version=9;`

const migrationV10 = `
ALTER TABLE run_controls RENAME TO run_controls_v9;
CREATE TABLE run_controls(request_id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id),
 task_id TEXT NOT NULL REFERENCES tasks(id), session_id TEXT NOT NULL REFERENCES execution_sessions(id),
 kind TEXT NOT NULL CHECK(kind IN ('wrap_up','instruction')), state TEXT NOT NULL, payload TEXT NOT NULL);
INSERT INTO run_controls SELECT * FROM run_controls_v9 ORDER BY rowid;
DROP TABLE run_controls_v9;
CREATE INDEX run_controls_task ON run_controls(task_id);
CREATE UNIQUE INDEX run_controls_wrap_up ON run_controls(run_id) WHERE kind='wrap_up';
PRAGMA user_version=10;`

func controlDigest(v model.ControlRecord) string {
	return model.RecoveryDigest([]any{v.Input, v.TaskID, v.ContractDigest, v.ProfileDigest, v.CreatedAt})
}
func validControl(v model.ControlRecord) bool {
	if !(model.ValidWrapUpInput(v.Input) || model.ValidInstructionInput(v.Input)) || !uuidRE.MatchString(v.TaskID) || !digestRE.MatchString(v.ContractDigest) || !digestRE.MatchString(v.ProfileDigest) || controlDigest(v) != v.Digest || !model.ValidControlState(v.State) {
		return false
	}
	start, e := time.Parse(time.RFC3339Nano, v.CreatedAt)
	end, e2 := time.Parse(time.RFC3339Nano, v.UpdatedAt)
	if e != nil || e2 != nil || end.Before(start) {
		return false
	}
	if v.State == model.ControlAcknowledged {
		return v.Disposition != nil && slices.Contains([]string{"queued", "handled"}, *v.Disposition) && v.Reason == nil
	}
	if v.Disposition != nil {
		return false
	}
	if v.State == model.ControlAccepted || v.State == model.ControlSending {
		return v.Reason == nil
	}
	return v.Reason != nil && model.ValidControlReason(*v.Reason)
}
func loadControl(q querier, id string) (model.ControlRecord, error) {
	var v model.ControlRecord
	var rid, tid, sid, kind, state string
	var raw []byte
	err := q.QueryRow("SELECT run_id,task_id,session_id,kind,state,payload FROM run_controls WHERE request_id=?", id).Scan(&rid, &tid, &sid, &kind, &state, &raw)
	if err == sql.ErrNoRows {
		return v, ErrNotFound
	}
	if err != nil || json.Unmarshal(raw, &v) != nil || !validControl(v) || v.Input.RequestID != id || v.Input.RunID != rid || v.Input.SessionID != sid || v.TaskID != tid || kind != model.ControlKind(v.Input) || v.State != state {
		return v, ErrConflict
	}
	return v, nil
}
func (s *Store) Control(id string) (model.ControlRecord, error) { return loadControl(s.rdb, id) }
func controlRun(st *model.State, id string) *model.Run {
	for i := range st.Runs {
		if st.Runs[i].ID == id {
			return &st.Runs[i]
		}
	}
	return nil
}
func controlBinding(q querier, v model.ControlRecord) error {
	st, err := readState(q)
	if err != nil {
		return err
	}
	r := controlRun(st, v.Input.RunID)
	t := taskByID(st, v.TaskID)
	ss, err := loadSession(q, v.Input.SessionID)
	if err != nil || t == nil || r == nil || r.TaskID != t.ID || ss.TaskID != t.ID || ss.ProfileID != r.ProfileID || ss.Role != r.Role || ss.ContractDigest != v.ContractDigest || ss.ProfileDigest != v.ProfileDigest || model.FrozenTaskDigest(st, *t) != v.ContractDigest {
		return ErrConflict
	}
	if model.ControlKind(v.Input) == "instruction" && r.Role != "developer" && r.Role != "polisher" {
		return ErrConflict
	}
	if !model.IsActiveRunState(r.State) || r.State == model.RunStopping || ss.State != model.SessionRunning || ss.ActiveRunID == nil || *ss.ActiveRunID != r.ID {
		return ErrUnknownRun
	}
	return nil
}
func putControl(tx *sql.Tx, v model.ControlRecord) error {
	if !validControl(v) {
		return ErrConflict
	}
	res, err := tx.Exec("UPDATE run_controls SET state=?,payload=? WHERE request_id=?", v.State, mustJSON(v), v.Input.RequestID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) RequestWrapUpOwned(token string, in model.WrapUpInput) (model.ControlRecord, error) {
	if !model.ValidWrapUpInput(in) {
		return model.ControlRecord{}, model.Invalidf("invalid wrap-up")
	}
	return s.requestControlOwned(token, in)
}
func (s *Store) RequestInstructionOwned(token string, in model.WrapUpInput) (model.ControlRecord, error) {
	if !model.ValidInstructionInput(in) {
		return model.ControlRecord{}, model.Invalidf("invalid instruction")
	}
	return s.requestControlOwned(token, in)
}
func (s *Store) requestControlOwned(token string, in model.WrapUpInput) (model.ControlRecord, error) {
	var out model.ControlRecord
	if !(model.ValidWrapUpInput(in) || model.ValidInstructionInput(in)) {
		return out, model.Invalidf("explicit control authorization required")
	}
	err := s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		old, err := loadControl(tx, in.RequestID)
		if err == nil {
			if !reflect.DeepEqual(old.Input, in) {
				return ErrConflict
			}
			out = old
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		var n int
		if tx.QueryRow("SELECT count(*) FROM stop_receipts WHERE request_id=?", in.RequestID).Scan(&n) != nil || n != 0 {
			return ErrConflict
		}
		if tx.QueryRow("SELECT count(*) FROM run_controls WHERE run_id=? AND kind=?", in.RunID, model.ControlKind(in)).Scan(&n) != nil || (model.ControlKind(in) == "wrap_up" && n != 0) || n >= 256 {
			return ErrConflict
		}
		ss, err := loadSession(tx, in.SessionID)
		if err != nil {
			return err
		}
		at := now()
		out = model.ControlRecord{Input: in, TaskID: ss.TaskID, ContractDigest: ss.ContractDigest, ProfileDigest: ss.ProfileDigest, State: model.ControlAccepted, CreatedAt: at, UpdatedAt: at}
		out.Digest = controlDigest(out)
		if err := controlBinding(tx, out); err != nil {
			return err
		}
		if !validControl(out) {
			return ErrConflict
		}
		_, err = tx.Exec("INSERT INTO run_controls(request_id,run_id,task_id,session_id,kind,state,payload) VALUES(?,?,?,?,?,?,?)", in.RequestID, in.RunID, out.TaskID, in.SessionID, model.ControlKind(in), out.State, mustJSON(out))
		return err
	})
	return out, err
}
func (s *Store) BeginControlOwned(token, id string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		v, err := loadControl(tx, id)
		if err != nil {
			return err
		}
		if v.State != model.ControlAccepted {
			return ErrConflict
		}
		if err := controlBinding(tx, v); err != nil {
			return err
		}
		v.State, v.UpdatedAt = model.ControlSending, now()
		return putControl(tx, v)
	})
}
func (s *Store) FinishControlOwned(token, id, state, disposition, reason string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		v, err := loadControl(tx, id)
		if err != nil {
			return err
		}
		if v.State != model.ControlSending && !(v.State == model.ControlAccepted && state == model.ControlRejected) {
			return ErrConflict
		}
		if !slices.Contains([]string{model.ControlAcknowledged, model.ControlRejected, model.ControlUnknown}, state) {
			return ErrConflict
		}
		v.State, v.UpdatedAt = state, now()
		if disposition != "" {
			v.Disposition = &disposition
		}
		if reason != "" {
			v.Reason = &reason
		}
		return putControl(tx, v)
	})
}
func controls(q querier, taskID string) ([]model.ControlRecord, error) {
	query := "SELECT request_id FROM run_controls ORDER BY rowid"
	var args []any
	if taskID != "" {
		query = "SELECT request_id FROM run_controls WHERE task_id=? ORDER BY rowid"
		args = []any{taskID}
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, ErrConflict
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []model.ControlRecord{}
	for _, id := range ids {
		v, err := loadControl(q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) PendingControls() ([]model.ControlRecord, error) {
	all, err := controls(s.rdb, "")
	return slices.DeleteFunc(all, func(v model.ControlRecord) bool { return v.State != model.ControlAccepted }), err
}
func (s *Store) CloseControlsOwned(token, runID string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		all, err := controls(tx, "")
		if err != nil {
			return err
		}
		for _, v := range all {
			if v.Input.RunID != runID {
				continue
			}
			switch v.State {
			case model.ControlAccepted:
				why := "run_ended_before_send"
				v.State, v.Reason = model.ControlRejected, &why
			case model.ControlSending:
				why := "protocol_reply_unknown"
				v.State, v.Reason = model.ControlUnknown, &why
			default:
				continue
			}
			v.UpdatedAt = now()
			if err := putControl(tx, v); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) ReconcileControlsOwned(token string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		all, err := controls(tx, "")
		if err != nil {
			return err
		}
		for _, v := range all {
			if v.State != model.ControlAccepted && v.State != model.ControlSending {
				continue
			}
			why := "controller_interrupted"
			v.State, v.Reason, v.UpdatedAt = model.ControlUnknown, &why, now()
			if err := putControl(tx, v); err != nil {
				return err
			}
		}
		return nil
	})
}
func controlReceipt(v model.ControlRecord, st *model.State) model.ControlReceipt {
	runState := model.RunUnknown
	var outcome *string
	if r := controlRun(st, v.Input.RunID); r != nil {
		runState = r.State
		if model.IsTerminalRunState(r.State) || r.State == model.RunUnknown {
			outcome = &r.State
		}
	}
	return model.ControlReceipt{RequestID: v.Input.RequestID, TaskID: v.TaskID, RunID: v.Input.RunID, SessionID: &v.Input.SessionID, Kind: model.ControlKind(v.Input), State: v.State, Disposition: v.Disposition, Reason: v.Reason, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, RunState: runState, Outcome: outcome}
}
func loadStop(q querier, id string) (model.StopReceipt, error) {
	v := model.StopReceipt{RequestID: id, Accepted: true}
	err := q.QueryRow("SELECT run_id,state,created_at,processed_at,outcome FROM stop_receipts WHERE request_id=?", id).Scan(&v.RunID, &v.State, &v.CreatedAt, &v.ProcessedAt, &v.Outcome)
	if err == sql.ErrNoRows {
		return v, ErrNotFound
	}
	return v, err
}
func stopControlReceipt(q querier, v model.StopReceipt, st *model.State) model.ControlReceipt {
	r := model.ControlReceipt{RequestID: v.RequestID, RunID: v.RunID, Kind: "stop", State: model.ControlAccepted, CreatedAt: v.CreatedAt, UpdatedAt: v.CreatedAt, RunState: model.RunUnknown, Outcome: v.Outcome}
	if v.State == model.StopProcessed {
		r.State = "processed"
		if v.Outcome == nil || *v.Outcome == model.RunUnknown {
			r.State = model.ControlUnknown
		}
		if v.ProcessedAt != nil {
			r.UpdatedAt = *v.ProcessedAt
		}
	}
	if run := controlRun(st, v.RunID); run != nil {
		r.TaskID, r.RunState = run.TaskID, run.State
	}
	if ss, err := sessionForRun(q, v.RunID); err == nil {
		r.SessionID = &ss.ID
	}
	return r
}
func (s *Store) ControlReceipt(id string) (model.ControlReceipt, error) {
	if !uuidRE.MatchString(id) {
		return model.ControlReceipt{}, model.Invalidf("request UUID required")
	}
	id = strings.ToLower(id)
	st, err := s.Read()
	if err != nil {
		return model.ControlReceipt{}, err
	}
	v, err := loadControl(s.rdb, id)
	if err == nil {
		return controlReceipt(v, st), nil
	}
	if err != ErrNotFound {
		return model.ControlReceipt{}, err
	}
	stop, err := loadStop(s.rdb, id)
	return stopControlReceipt(s.rdb, stop, st), err
}
func (s *Store) ControlSummaries(taskID string, st *model.State) ([]model.ControlReceipt, error) {
	all, err := controls(s.rdb, taskID)
	if err != nil {
		return nil, err
	}
	out := []model.ControlReceipt{}
	for _, v := range all {
		out = append(out, controlReceipt(v, st))
	}
	rows, err := s.rdb.Query("SELECT request_id FROM stop_receipts WHERE run_id IN (SELECT id FROM runs WHERE task_id=?)", taskID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, ErrConflict
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		v, err := loadStop(s.rdb, id)
		if err != nil {
			return nil, err
		}
		out = append(out, stopControlReceipt(s.rdb, v, st))
	}
	slices.SortFunc(out, func(a, b model.ControlReceipt) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	if len(out) > 50 {
		out = out[len(out)-50:]
	}
	return out, nil
}
func validateControlsBackup(q querier) error {
	all, err := controls(q, "")
	if err != nil {
		return ErrBadBackup
	}
	st, err := readState(q)
	if err != nil {
		return ErrBadBackup
	}
	for _, v := range all {
		r := controlRun(st, v.Input.RunID)
		t := taskByID(st, v.TaskID)
		ss, err := loadSession(q, v.Input.SessionID)
		if err != nil || r == nil || t == nil || r.TaskID != t.ID || ss.TaskID != t.ID || ss.ProfileID != r.ProfileID || ss.Role != r.Role || ss.ContractDigest != v.ContractDigest || ss.ProfileDigest != v.ProfileDigest || model.FrozenTaskDigest(st, *t) != v.ContractDigest {
			return ErrBadBackup
		}
		var n int
		if q.QueryRow("SELECT count(*) FROM stop_receipts WHERE request_id=?", v.Input.RequestID).Scan(&n) != nil || n != 0 {
			return ErrBadBackup
		}
	}
	return nil
}
