package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

const migrationV4 = `
CREATE TABLE execution_sessions (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) DEFERRABLE INITIALLY DEFERRED,
  state TEXT NOT NULL, active_run_id TEXT REFERENCES runs(id) DEFERRABLE INITIALLY DEFERRED, payload TEXT NOT NULL);
CREATE INDEX sessions_task ON execution_sessions(task_id);
CREATE UNIQUE INDEX session_task_owner ON execution_sessions(task_id) WHERE state = 'running';
CREATE TABLE session_runs (run_id TEXT PRIMARY KEY REFERENCES runs(id) DEFERRABLE INITIALLY DEFERRED,
  session_id TEXT NOT NULL REFERENCES execution_sessions(id) DEFERRABLE INITIALLY DEFERRED);
PRAGMA user_version = 4;
`

var sessionTables = []string{"execution_sessions", "session_runs"}

func SessionFileRef(id string) string {
	return filepath.ToSlash(filepath.Join("sessions", id, "history.jsonl"))
}

func validSession(s model.Session) error {
	if !uuidRE.MatchString(s.ID) || !uuidRE.MatchString(s.TaskID) || !model.IsRole(s.Role) || s.Executor == "" || s.ProfileID == "" ||
		!digestRE.MatchString(s.ProfileDigest) || !digestRE.MatchString(s.ContractDigest) || !digestRE.MatchString(s.FileDigest) ||
		s.ProviderID == "" || len(s.ProviderID) > 128 || model.LooksLikeCredential(s.ProviderID) || s.FileRef != SessionFileRef(s.ID) ||
		!operationSHARE.MatchString(s.LastSHA) || !slices.Contains([]string{model.SessionIdle, model.SessionRunning, model.SessionUnknown}, s.State) {
		return model.Invalidf("invalid session record")
	}
	for _, at := range []string{s.CreatedAt, s.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			return model.Invalidf("invalid session time")
		}
	}
	if s.State == model.SessionIdle && s.ActiveRunID != nil || s.State == model.SessionRunning && s.ActiveRunID == nil || s.ActiveRunID != nil && !uuidRE.MatchString(*s.ActiveRunID) {
		return model.Invalidf("invalid session ownership")
	}
	return nil
}

func loadSession(q querier, id string) (model.Session, error) {
	var s model.Session
	var taskID, state string
	var run sql.NullString
	var payload []byte
	err := q.QueryRow("SELECT task_id,state,active_run_id,payload FROM execution_sessions WHERE id=?", id).Scan(&taskID, &state, &run, &payload)
	if err == sql.ErrNoRows {
		return s, ErrNotFound
	}
	if err != nil || json.Unmarshal(payload, &s) != nil || validSession(s) != nil || s.ID != id || s.TaskID != taskID || s.State != state || run.Valid != (s.ActiveRunID != nil) || run.Valid && run.String != *s.ActiveRunID {
		return model.Session{}, fmt.Errorf("store: session corrupt")
	}
	if run.Valid {
		var sid string
		if q.QueryRow("SELECT session_id FROM session_runs WHERE run_id=?", run.String).Scan(&sid) != nil || sid != id {
			return model.Session{}, fmt.Errorf("store: session ownership corrupt")
		}
	}
	return s, nil
}

func sessions(q querier, taskID string) ([]model.Session, error) {
	query := "SELECT id FROM execution_sessions ORDER BY rowid"
	args := []any{}
	if taskID != "" {
		query = "SELECT id FROM execution_sessions WHERE task_id=? ORDER BY rowid"
		args = append(args, taskID)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: sessions unreadable")
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, fmt.Errorf("store: sessions unreadable")
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("store: sessions unreadable")
	}
	out := []model.Session{}
	for _, id := range ids {
		s, err := loadSession(q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (s *Store) SessionsForTask(taskID string) ([]model.Session, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: sessions read failed")
	}
	defer tx.Rollback()
	return sessions(tx, taskID)
}

func (s *Store) SessionForRun(runID string) (model.Session, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return model.Session{}, fmt.Errorf("store: session read failed")
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRow("SELECT session_id FROM session_runs WHERE run_id=?", runID).Scan(&id)
	if err == sql.ErrNoRows {
		return model.Session{}, ErrNotFound
	}
	if err != nil {
		return model.Session{}, fmt.Errorf("store: session read failed")
	}
	return loadSession(tx, id)
}

func immutableSession(a, b model.Session) bool {
	return a.ID == b.ID && a.TaskID == b.TaskID && a.Role == b.Role && a.Executor == b.Executor && a.ProfileID == b.ProfileID && a.ProfileDigest == b.ProfileDigest &&
		a.ContractDigest == b.ContractDigest && a.ProviderID == b.ProviderID && a.FileRef == b.FileRef && a.CreatedAt == b.CreatedAt
}

func putSession(tx *sql.Tx, s model.Session) error {
	if err := validSession(s); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO execution_sessions(id,task_id,state,active_run_id,payload) VALUES(?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state,active_run_id=excluded.active_run_id,payload=excluded.payload`, s.ID, s.TaskID, s.State, s.ActiveRunID, mustJSON(s)); err != nil {
		return fmt.Errorf("%w: session ownership", ErrConflict)
	}
	return nil
}

// StartSessionRunOwned atomically records the run and its exclusive history
// claim. File inspection and executor/network calls must happen before this.
func (s *Store) StartSessionRunOwned(token string, session model.Session, runID string, fresh bool, fn func(*model.State) error, saved ...model.Checkpoint) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		old, err := loadSession(tx, session.ID)
		if fresh {
			if err != ErrNotFound {
				return ErrConflict
			}
		} else if err != nil || !immutableSession(old, session) || old.State != model.SessionIdle || old.ActiveRunID != nil || old.FileDigest != session.FileDigest || old.LastSHA != session.LastSHA {
			return ErrConflict
		}
		if session.State != model.SessionIdle || session.ActiveRunID != nil || !uuidRE.MatchString(runID) {
			return ErrConflict
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := fn(st); err != nil {
			return err
		}
		var worktree string
		for _, task := range st.Tasks {
			if task.ID == session.TaskID {
				worktree = task.Worktree
			}
		}
		var occupied int
		if tx.QueryRow("SELECT count(*) FROM worktree_checkpoints WHERE state='saved' AND worktree=? AND task_id!=?", worktree, session.TaskID).Scan(&occupied) != nil || occupied > 0 {
			return ErrConflict
		}
		if len(saved) > 1 {
			return ErrConflict
		}
		var ownSaved int
		if tx.QueryRow("SELECT count(*) FROM worktree_checkpoints WHERE state='saved' AND task_id=?", session.TaskID).Scan(&ownSaved) != nil || ownSaved != len(saved) {
			return ErrConflict
		}
		if len(saved) == 1 {
			if fresh {
				return ErrConflict
			}
			if err := consumeCheckpoint(tx, saved[0], session, runID); err != nil {
				return err
			}
		}
		if err := writeState(tx, st); err != nil {
			return err
		}
		session.State, session.ActiveRunID, session.UpdatedAt = model.SessionRunning, &runID, now()
		if err := putSession(tx, session); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO session_runs(run_id,session_id) VALUES(?,?)", runID, session.ID); err != nil {
			return ErrConflict
		}
		return nil
	})
}

// FinishSessionRunOwned settles the run and history together. An uncertain
// process/file remains unknown; it never becomes a resumable idle session.
func (s *Store) FinishSessionRunOwned(token string, session model.Session, runID string, fn func(*model.State) error, saved ...model.Checkpoint) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		old, err := loadSession(tx, session.ID)
		if err != nil || !immutableSession(old, session) || old.State != model.SessionRunning || old.ActiveRunID == nil || *old.ActiveRunID != runID ||
			(session.State != model.SessionIdle && session.State != model.SessionUnknown) || session.ActiveRunID != nil {
			return ErrConflict
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := fn(st); err != nil {
			return err
		}
		if err := writeState(tx, st); err != nil {
			return err
		}
		if err := putSession(tx, session); err != nil {
			return err
		}
		if len(saved) > 1 {
			return ErrConflict
		}
		if len(saved) == 1 {
			return putCheckpoint(tx, saved[0], st, session)
		}
		return nil
	})
}

func (s *Store) ReconcileSessionsOwned(token string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		all, err := sessions(tx, "")
		if err != nil {
			return err
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		changed := false
		for _, ss := range all {
			if ss.State != model.SessionRunning {
				continue
			}
			ss.State, ss.UpdatedAt = model.SessionUnknown, now()
			if err := putSession(tx, ss); err != nil {
				return err
			}
			for i := range st.Tasks {
				if st.Tasks[i].ID == ss.TaskID {
					if st.Tasks[i].State != model.TaskUnknown {
						st.Tasks[i].RecordedState = st.Tasks[i].State
					}
					reason := "session_restart_unverified"
					st.Tasks[i].State, st.Tasks[i].StateReason, st.Tasks[i].UpdatedAt = model.TaskUnknown, &reason, ss.UpdatedAt
				}
			}
			changed = true
		}
		if changed {
			return writeState(tx, st)
		}
		return nil
	})
}
