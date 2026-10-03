package store

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

const migrationV8 = `
CREATE TABLE run_completions (run_id TEXT PRIMARY KEY REFERENCES runs(id),
 task_id TEXT NOT NULL REFERENCES tasks(id), session_id TEXT NOT NULL REFERENCES execution_sessions(id),
 state TEXT NOT NULL, digest TEXT NOT NULL, payload TEXT NOT NULL);
CREATE TABLE recovery_decisions (request_id TEXT PRIMARY KEY, run_id TEXT NOT NULL UNIQUE REFERENCES run_completions(run_id),
 task_id TEXT NOT NULL REFERENCES tasks(id), payload TEXT NOT NULL);
PRAGMA user_version = 8;`

var recoveryTables = []string{"run_completions", "recovery_decisions"}

func validCompletion(e model.CompletionEvidence, st *model.State) bool {
	a, b := e.BeforeRun, e.AfterRun
	x, y := e.BeforeSession, e.AfterSession
	if e.SchemaVersion != 1 || !uuidRE.MatchString(a.ID) || a.ID != b.ID || a.TaskID != e.BeforeTask.ID || a.TaskID != e.AfterTask.ID || a.TaskID != x.TaskID || x.TaskID != y.TaskID ||
		!model.IsActiveRunState(a.State) || b.State != model.RunSucceeded || b.EndedAt == nil || b.Usage == nil || b.Usage.Validate() != nil || b.Usage.Tokens.Total == nil ||
		a.Role != b.Role || a.Executor != b.Executor || a.ProfileID != b.ProfileID || !reflect.DeepEqual(a.ContextRef, b.ContextRef) || a.StartedAt != b.StartedAt ||
		!reflect.DeepEqual(a.PID, b.PID) || a.Host != b.Host || a.ProcessStartedAt != b.ProcessStartedAt || e.ProcessGroupID < 0 || a.PID != nil && (*a.PID <= 0 || e.ProcessGroupID != *a.PID || a.Host == "" || a.ProcessStartedAt == "") || a.PID == nil && e.ProcessGroupID != 0 ||
		validSession(x) != nil || validSession(y) != nil || !immutableSession(x, y) || x.State != model.SessionRunning || x.ActiveRunID == nil || *x.ActiveRunID != a.ID || y.State != model.SessionIdle || y.ActiveRunID != nil ||
		e.AfterTask.CandidateSha == nil || *e.AfterTask.CandidateSha != y.LastSHA || !operationSHARE.MatchString(y.LastSHA) ||
		model.FrozenTaskDigest(st, e.BeforeTask) != e.ContractDigest || model.FrozenTaskDigest(st, e.AfterTask) != e.ContractDigest || x.ContractDigest != e.ContractDigest ||
		len(e.Deliveries) > 2 || len(e.Reviews) > 1 {
		return false
	}
	for _, at := range []string{e.CreatedAt, a.StartedAt, *b.EndedAt} {
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			return false
		}
	}
	start, _ := time.Parse(time.RFC3339Nano, a.StartedAt)
	end, _ := time.Parse(time.RFC3339Nano, *b.EndedAt)
	if end.Before(start) {
		return false
	}
	var before, after map[string]any
	if json.Unmarshal(a.Summary, &before) != nil || json.Unmarshal(b.Summary, &after) != nil || after["outcome"] != "success" || after["resultSha"] != y.LastSHA {
		return false
	}
	for _, key := range []string{"purpose", "limits", "fixRound", "budgetRevision"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			return false
		}
	}
	for _, d := range e.Deliveries {
		if !uuidRE.MatchString(d.ID) || d.TaskID != a.TaskID || d.CandidateSha != y.LastSHA || d.ContextRef != e.AfterTask.ContextRef {
			return false
		}
	}
	for _, r := range e.Reviews {
		if !uuidRE.MatchString(r.ID) || r.TaskID != a.TaskID || r.RunID != a.ID || r.CandidateSha != y.LastSHA || r.ContextDigest != e.AfterTask.ContextRef.Digest {
			return false
		}
	}
	return len(mustJSON(e)) <= 2<<20
}
func loadCompletion(q querier, id string) (model.CompletionRecord, error) {
	st, err := readState(q)
	if err != nil {
		return model.CompletionRecord{}, err
	}
	return loadCompletionInState(q, id, st)
}
func loadCompletionInState(q querier, id string, st *model.State) (model.CompletionRecord, error) {
	var v model.CompletionRecord
	var tid, sid string
	var raw []byte
	e := q.QueryRow("SELECT task_id,session_id,state,digest,payload FROM run_completions WHERE run_id=?", id).Scan(&tid, &sid, &v.State, &v.Digest, &raw)
	if e == sql.ErrNoRows {
		return v, ErrNotFound
	}
	if e != nil || json.Unmarshal(raw, &v.Evidence) != nil || v.Evidence.BeforeRun.ID != id || v.Evidence.BeforeTask.ID != tid || v.Evidence.BeforeSession.ID != sid || model.RecoveryDigest(v.Evidence) != v.Digest || !slices.Contains([]string{"pending", "settled", "recovered"}, v.State) {
		return v, ErrConflict
	}
	if !validCompletion(v.Evidence, st) {
		return v, ErrConflict
	}
	return v, nil
}
func (s *Store) Completion(runID string) (model.CompletionRecord, error) {
	return loadCompletion(s.rdb, runID)
}
func recoveryBudget(q querier, tid string) error {
	cs, e := controls(q, tid)
	if e != nil {
		return e
	}
	for _, v := range cs {
		if v.State == model.ControlAccepted || v.State == model.ControlSending || v.State == model.ControlUnknown {
			return budget.ErrUnknown
		}
	}
	ps, rs, e := budgetRows(q, tid)
	if e != nil {
		return e
	}
	for _, p := range ps {
		if p.State != "closed" {
			return budget.ErrUnknown
		}
	}
	for _, r := range rs {
		if r.Overrun {
			return budget.ErrDenied
		}
		if r.State != budget.Settled && r.State != budget.Canceled {
			return budget.ErrUnknown
		}
	}
	return nil
}
func sameRecoveryTask(a, b model.Task) bool {
	a.State, a.RecordedState, a.StateReason, a.UpdatedAt = "", "", nil, ""
	b.State, b.RecordedState, b.StateReason, b.UpdatedAt = "", "", nil, ""
	return reflect.DeepEqual(a, b)
}
func sameRecoveryRun(a, b model.Run) bool {
	a.State, a.RecordedState, a.UpdatedAt, a.HeartbeatAt, a.Events, a.LeaseToken = "", "", "", "", nil, ""
	b.State, b.RecordedState, b.UpdatedAt, b.HeartbeatAt, b.Events, b.LeaseToken = "", "", "", "", nil, ""
	return reflect.DeepEqual(a, b)
}
func sameRecoverySession(a, b model.Session) bool {
	a.State, a.UpdatedAt = "", ""
	b.State, b.UpdatedAt = "", ""
	return reflect.DeepEqual(a, b)
}
func completionCurrent(q querier, st *model.State, v model.CompletionRecord, unknown bool) error {
	e := v.Evidence
	t := taskByID(st, e.BeforeTask.ID)
	var r *model.Run
	for i := range st.Runs {
		if st.Runs[i].ID == e.BeforeRun.ID {
			r = &st.Runs[i]
		}
	}
	ss, err := loadSession(q, e.BeforeSession.ID)
	if err != nil || t == nil || r == nil || !sameRecoveryTask(*t, e.BeforeTask) || !sameRecoveryRun(*r, e.BeforeRun) || !sameRecoverySession(ss, e.BeforeSession) {
		return ErrConflict
	}
	if unknown {
		if t.State != model.TaskUnknown || r.State != model.RunUnknown || ss.State != model.SessionUnknown {
			return ErrConflict
		}
		var n int
		if q.QueryRow("SELECT count(*) FROM worktree_claims WHERE task_id=? OR worktree=?", t.ID, t.Worktree).Scan(&n) != nil || n != 0 {
			return ErrConflict
		}
	} else if t.State != e.BeforeTask.State || r.State != e.BeforeRun.State || ss.State != model.SessionRunning {
		return ErrConflict
	}
	for _, run := range st.Runs {
		if run.TaskID == t.ID && run.ID != r.ID && (model.IsActiveRunState(run.State) || run.State == model.RunUnknown) {
			return ErrUnknownRun
		}
	}
	all, err := sessions(q, t.ID)
	if err != nil {
		return err
	}
	for _, s := range all {
		if s.ID != ss.ID && s.State != model.SessionIdle {
			return ErrUnknownRun
		}
	}
	return recoveryBudget(q, t.ID)
}

// StageCompletionOwned persists evidence before the final settlement transaction.
func (s *Store) StageCompletionOwned(token string, e model.CompletionEvidence) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if !validCompletion(e, st) {
			return ErrConflict
		}
		v := model.CompletionRecord{Evidence: e, Digest: model.RecoveryDigest(e), State: "pending"}
		if err := completionCurrent(tx, st, v, false); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO run_completions(run_id,task_id,session_id,state,digest,payload) VALUES(?,?,?,?,?,?)", e.BeforeRun.ID, e.BeforeTask.ID, e.BeforeSession.ID, v.State, v.Digest, mustJSON(e))
		return err
	})
}
func applyCompletion(tx *sql.Tx, st *model.State, v model.CompletionRecord, recovered bool) error {
	e := v.Evidence
	t := taskByID(st, e.BeforeTask.ID)
	if t == nil {
		return ErrNotFound
	}
	*t = e.AfterTask
	if recovered && t.Origin != delegateOrigin {
		t.State = model.TaskStopped
		why := "verified_completion_recovered"
		t.StateReason = &why
		t.UpdatedAt = now()
	}
	for i := range st.Runs {
		if st.Runs[i].ID == e.BeforeRun.ID {
			st.Runs[i] = e.AfterRun
		}
	}
	for _, d := range e.Deliveries {
		if slices.ContainsFunc(st.Deliveries, func(x model.Delivery) bool { return x.ID == d.ID }) {
			return ErrConflict
		}
		st.Deliveries = append(st.Deliveries, d)
	}
	for _, r := range e.Reviews {
		if slices.ContainsFunc(st.Reviews, func(x model.Review) bool { return x.ID == r.ID }) {
			return ErrConflict
		}
		st.Reviews = append(st.Reviews, r)
	}
	if err := writeState(tx, st); err != nil {
		return err
	}
	if err := putSession(tx, e.AfterSession); err != nil {
		return err
	}
	state := "settled"
	if recovered {
		state = "recovered"
	}
	res, err := tx.Exec("UPDATE run_completions SET state=? WHERE run_id=? AND state='pending'", state, e.BeforeRun.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) FinishCompletionOwned(token, id string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		v, err := loadCompletion(tx, id)
		if err != nil {
			return err
		}
		if v.State != "pending" {
			return ErrConflict
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := completionCurrent(tx, st, v, false); err != nil {
			return err
		}
		return applyCompletion(tx, st, v, false)
	})
}
func recoveryProposal(q querier, taskID, runID string) (model.RecoveryProposal, error) {
	v, err := loadCompletion(q, runID)
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	if v.State != "pending" || v.Evidence.BeforeTask.ID != taskID {
		return model.RecoveryProposal{}, ErrConflict
	}
	st, err := readState(q)
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	if err := completionCurrent(q, st, v, true); err != nil {
		return model.RecoveryProposal{}, err
	}
	ps, rs, err := budgetRows(q, taskID)
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	ss, err := sessions(q, taskID)
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	cps, err := checkpoints(q, taskID)
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	var runs []model.Run
	var ds []model.Delivery
	var reviews []model.Review
	for _, r := range st.Runs {
		if r.TaskID == taskID {
			runs = append(runs, r)
		}
	}
	for _, d := range st.Deliveries {
		if d.TaskID == taskID {
			ds = append(ds, d)
		}
	}
	for _, r := range st.Reviews {
		if r.TaskID == taskID {
			reviews = append(reviews, r)
		}
	}
	cs, err := controls(q, taskID)
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	return model.RecoveryProposal{SchemaVersion: 1, TaskID: taskID, RunID: runID, CompletionDigest: v.Digest, EvidenceDigest: model.RecoveryDigest([]any{taskByID(st, taskID), runs, ss, ps, rs, cps, ds, reviews, cs})}, nil
}
func (s *Store) ProposeRecovery(taskID, runID string) (model.RecoveryProposal, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return model.RecoveryProposal{}, err
	}
	defer tx.Rollback()
	return recoveryProposal(tx, taskID, runID)
}
func loadRecoveryDecision(q querier, id string) (model.RecoveryDecision, error) {
	var v model.RecoveryDecision
	var rid, tid string
	var raw []byte
	err := q.QueryRow("SELECT run_id,task_id,payload FROM recovery_decisions WHERE request_id=?", id).Scan(&rid, &tid, &raw)
	if err == sql.ErrNoRows {
		return v, ErrNotFound
	}
	if err != nil || json.Unmarshal(raw, &v) != nil || !model.ValidRecoveryInput(v.RecoveryInput) || v.RequestID != id || v.Proposal.RunID != rid || v.Proposal.TaskID != tid {
		return v, ErrConflict
	}
	if _, err := time.Parse(time.RFC3339Nano, v.CreatedAt); err != nil {
		return v, ErrConflict
	}
	return v, nil
}
func (s *Store) RecoveryDecision(id string) (model.RecoveryDecision, error) {
	return loadRecoveryDecision(s.rdb, id)
}
func (s *Store) ApplyRecoveryOwned(token string, in model.RecoveryInput) (model.RecoveryDecision, error) {
	var out model.RecoveryDecision
	if !model.ValidRecoveryInput(in) {
		return out, model.Invalidf("explicit recovery authorization required")
	}
	err := s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		old, err := loadRecoveryDecision(tx, in.RequestID)
		if err == nil {
			if !reflect.DeepEqual(old.RecoveryInput, in) {
				return ErrConflict
			}
			out = old
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		p, err := recoveryProposal(tx, in.Proposal.TaskID, in.Proposal.RunID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(p, in.Proposal) {
			return ErrConflict
		}
		v, err := loadCompletion(tx, p.RunID)
		if err != nil {
			return err
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := applyCompletion(tx, st, v, true); err != nil {
			return err
		}
		out = model.RecoveryDecision{RecoveryInput: in, CreatedAt: now()}
		_, err = tx.Exec("INSERT INTO recovery_decisions(request_id,run_id,task_id,payload) VALUES(?,?,?,?)", in.RequestID, p.RunID, p.TaskID, mustJSON(out))
		return err
	})
	return out, err
}
func (s *Store) RecoverySummaries(taskID string) ([]model.RecoverySummary, error) {
	rows, err := s.rdb.Query("SELECT run_id FROM run_completions WHERE task_id=? ORDER BY rowid", taskID)
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
	out := []model.RecoverySummary{}
	if len(ids) == 0 {
		return out, nil
	}
	st, err := readState(s.rdb)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		v, err := loadCompletionInState(s.rdb, id, st)
		if err != nil {
			return nil, err
		}
		e := v.Evidence
		r := model.RecoverySummary{RunID: id, Role: e.AfterRun.Role, State: v.State, CandidateSHA: e.AfterSession.LastSHA, RecordedAt: e.CreatedAt}
		if v.State == "recovered" {
			var request string
			if s.rdb.QueryRow("SELECT request_id FROM recovery_decisions WHERE run_id=?", id).Scan(&request) != nil {
				return nil, ErrConflict
			}
			d, err := loadRecoveryDecision(s.rdb, request)
			if err != nil {
				return nil, err
			}
			r.RequestID = &d.RequestID
			r.RecoveredAt = &d.CreatedAt
		}
		out = append(out, r)
	}
	return out, nil
}
func validateRecoveryBackup(q querier) error {
	rows, err := q.Query("SELECT run_id FROM run_completions")
	if err != nil {
		return ErrBadBackup
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return ErrBadBackup
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrBadBackup
	}
	st, err := readState(q)
	if err != nil {
		return ErrBadBackup
	}
	for _, id := range ids {
		v, err := loadCompletionInState(q, id, st)
		if err != nil {
			return ErrBadBackup
		}
		var n int
		if q.QueryRow("SELECT count(*) FROM recovery_decisions WHERE run_id=?", id).Scan(&n) != nil || (v.State == "recovered") != (n == 1) || n > 1 {
			return ErrBadBackup
		}
		if n == 1 {
			var request string
			q.QueryRow("SELECT request_id FROM recovery_decisions WHERE run_id=?", id).Scan(&request)
			d, err := loadRecoveryDecision(q, request)
			if err != nil || d.Proposal.CompletionDigest != v.Digest || d.Proposal.TaskID != v.Evidence.BeforeTask.ID {
				return ErrBadBackup
			}
		}
	}
	return nil
}
