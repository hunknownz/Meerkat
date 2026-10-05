package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/platform"
)

const migrationV6 = `
CREATE TABLE worktree_checkpoints (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id),
 run_id TEXT NOT NULL UNIQUE REFERENCES runs(id), session_id TEXT NOT NULL REFERENCES execution_sessions(id),
 worktree TEXT NOT NULL, state TEXT NOT NULL, payload TEXT NOT NULL);
CREATE INDEX checkpoints_task ON worktree_checkpoints(task_id);
CREATE UNIQUE INDEX checkpoint_worktree_owner ON worktree_checkpoints(worktree) WHERE state='saved';
PRAGMA user_version = 6;
`
const checkpointTable = "worktree_checkpoints"
const checkpointBundleTable = "backup_checkpoint_files"

func CheckpointFileRef(id string) string { return "checkpoints/" + id + "/snapshot.json" }
func validCheckpoint(c model.Checkpoint) error {
	if !uuidRE.MatchString(c.ID) || !uuidRE.MatchString(c.TaskID) || !uuidRE.MatchString(c.RunID) || !uuidRE.MatchString(c.SessionID) ||
		!digestRE.MatchString(c.SessionDigest) || !digestRE.MatchString(c.ContractDigest) || !digestRE.MatchString(c.ProfileDigest) || !digestRE.MatchString(c.FileDigest) ||
		(c.Role != "developer" && c.Role != "polisher") || !filepath.IsAbs(c.Worktree) || c.FileRef != CheckpointFileRef(c.ID) ||
		(c.Role == "developer" && c.Purpose != "implement" && c.Purpose != "fix") || (c.Role == "polisher" && c.Purpose != "polish") ||
		!operationSHARE.MatchString(c.HeadSHA) || !operationSHARE.MatchString(c.BaselineSHA) || c.Branch == "" || c.FileCount < 0 || c.FileCount > 512 || c.LastEventSeq < 0 || c.LastEventSeq > model.MaxRunEvents ||
		(c.State != "saved" && c.State != "consumed") || (c.State == "saved" && c.ResumedRunID != nil) || (c.State == "consumed" && (c.ResumedRunID == nil || !uuidRE.MatchString(*c.ResumedRunID))) {
		return model.Invalidf("invalid checkpoint")
	}
	if _, e := time.Parse(time.RFC3339Nano, c.CreatedAt); e != nil {
		return model.Invalidf("invalid checkpoint time")
	}
	return nil
}

func checkpoints(q querier, taskID string) ([]model.Checkpoint, error) {
	query := "SELECT id,task_id,run_id,session_id,worktree,state,payload FROM worktree_checkpoints"
	args := []any{}
	if taskID != "" {
		query += " WHERE task_id=?"
		args = append(args, taskID)
	}
	query += " ORDER BY rowid"
	rows, e := q.Query(query, args...)
	if e != nil {
		return nil, fmt.Errorf("store: checkpoints unreadable")
	}
	defer rows.Close()
	out := []model.Checkpoint{}
	for rows.Next() {
		var c model.Checkpoint
		var id, tid, rid, sid, wt, state string
		var raw []byte
		if rows.Scan(&id, &tid, &rid, &sid, &wt, &state, &raw) != nil || json.Unmarshal(raw, &c) != nil || validCheckpoint(c) != nil || c.ID != id || c.TaskID != tid || c.RunID != rid || c.SessionID != sid || c.Worktree != wt || c.State != state {
			return nil, fmt.Errorf("store: checkpoint corrupt")
		}
		out = append(out, c)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("store: checkpoints unreadable")
	}
	return out, nil
}
func (s *Store) CheckpointsForTask(taskID string) ([]model.Checkpoint, error) {
	return checkpoints(s.rdb, taskID)
}

func (s *Store) WriteCheckpointFile(c model.Checkpoint, raw []byte) error {
	if validCheckpoint(c) != nil || checkpoint.Digest(raw) != c.FileDigest {
		return ErrConflict
	}
	ss, e := checkpoint.Decode(raw)
	if e != nil || ss.Head != c.HeadSHA || ss.Branch != c.Branch || ss.Worktree != c.Worktree || len(ss.Files) != c.FileCount {
		return ErrConflict
	}
	root := filepath.Join(s.dir, "checkpoints")
	if e := checkDir(root); e != nil {
		return e
	}
	dir := filepath.Join(root, c.ID)
	if platform.Mkdir(dir, 0o700) != nil {
		return ErrConflict
	}
	f, e := platform.OpenFile(filepath.Join(dir, "snapshot.tmp"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return ErrConflict
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return ErrConflict
	}
	if os.Rename(filepath.Join(dir, "snapshot.tmp"), filepath.Join(dir, "snapshot.json")) != nil {
		return ErrConflict
	}
	for _, p := range []string{dir, root, s.dir} {
		e = platform.SyncDir(p)
		if e != nil {
			return ErrConflict
		}
	}
	return nil
}
func (s *Store) ReadCheckpointFile(c model.Checkpoint) ([]byte, error) {
	if validCheckpoint(c) != nil {
		return nil, ErrConflict
	}
	for _, p := range []string{filepath.Join(s.dir, "checkpoints"), filepath.Join(s.dir, "checkpoints", c.ID)} {
		fi, e := os.Lstat(p)
		if e != nil || !fi.IsDir() || !platform.Private(p, fi, 0o700) {
			return nil, ErrUnsafeDir
		}
	}
	raw, e := readSafe(filepath.Join(s.dir, filepath.FromSlash(c.FileRef)), checkpoint.MaxBytes)
	ss, de := checkpoint.Decode(raw)
	if e != nil || de != nil || checkpoint.Digest(raw) != c.FileDigest || ss.Head != c.HeadSHA || ss.Branch != c.Branch || ss.Worktree != c.Worktree || len(ss.Files) != c.FileCount {
		return nil, ErrConflict
	}
	return raw, nil
}

// putCheckpoint runs after the run and session have settled in the same tx.
func putCheckpoint(tx *sql.Tx, c model.Checkpoint, st *model.State, ss model.Session) error {
	if validCheckpoint(c) != nil || c.State != "saved" || ss.State != model.SessionIdle || ss.ID != c.SessionID || ss.FileDigest != c.SessionDigest || ss.TaskID != c.TaskID || ss.ContractDigest != c.ContractDigest || ss.ProfileDigest != c.ProfileDigest || ss.LastSHA != c.HeadSHA {
		return ErrConflict
	}
	var task *model.Task
	var run *model.Run
	for i := range st.Tasks {
		if st.Tasks[i].ID == c.TaskID {
			task = &st.Tasks[i]
		}
	}
	for i := range st.Runs {
		if st.Runs[i].ID == c.RunID {
			run = &st.Runs[i]
		}
	}
	if task == nil || task.State != model.TaskPaused || task.Worktree != c.Worktree || run == nil || run.State != model.RunStopped || run.Role != c.Role || run.Usage == nil || run.Usage.Tokens.Total == nil {
		return ErrConflict
	}
	_, e := tx.Exec("INSERT INTO worktree_checkpoints(id,task_id,run_id,session_id,worktree,state,payload) VALUES(?,?,?,?,?,?,?)", c.ID, c.TaskID, c.RunID, c.SessionID, c.Worktree, c.State, mustJSON(c))
	if e != nil {
		return ErrConflict
	}
	return nil
}

func consumeCheckpoint(tx *sql.Tx, c model.Checkpoint, ss model.Session, runID string) error {
	all, e := checkpoints(tx, c.TaskID)
	if e != nil {
		return e
	}
	for _, old := range all {
		if old.ID == c.ID {
			if old.State != "saved" || string(mustJSON(old)) != string(mustJSON(c)) || ss.ID != c.SessionID || ss.FileDigest != c.SessionDigest || ss.LastSHA != c.HeadSHA {
				return ErrConflict
			}
			c.State, c.ResumedRunID = "consumed", &runID
			_, e := tx.Exec("UPDATE worktree_checkpoints SET state=?,payload=? WHERE id=? AND state='saved'", c.State, mustJSON(c), c.ID)
			return e
		}
	}
	return ErrConflict
}

func (s *Store) bundleCheckpoints(dest string) error {
	db, e := sql.Open("sqlite", dsn(dest, dsnBackupFile))
	if e != nil {
		return ErrBadBackup
	}
	defer db.Close()
	tx, e := db.Begin()
	if e != nil {
		return ErrBadBackup
	}
	defer tx.Rollback()
	all, e := checkpoints(tx, "")
	if e != nil {
		return e
	}
	if _, e = tx.Exec("CREATE TABLE " + checkpointBundleTable + " (id TEXT PRIMARY KEY,body BLOB NOT NULL)"); e != nil {
		return e
	}
	for _, c := range all {
		raw, e := s.ReadCheckpointFile(c)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO "+checkpointBundleTable+" VALUES(?,?)", c.ID, raw); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func verifyCheckpointBundle(q querier, fn func(model.Checkpoint, []byte) error) error {
	all, e := checkpoints(q, "")
	if e != nil {
		return ErrBadBackup
	}
	var exists int
	if q.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", checkpointBundleTable).Scan(&exists) != nil {
		return ErrBadBackup
	}
	if exists == 0 {
		if len(all) > 0 {
			return ErrBadBackup
		}
		return nil
	}
	st, e := readState(q)
	if e != nil {
		return ErrBadBackup
	}
	var n, bad int
	if q.QueryRow("SELECT count(*),coalesce(sum(typeof(body)!='blob' OR length(body)>?),0) FROM "+checkpointBundleTable, checkpoint.MaxBytes).Scan(&n, &bad) != nil || n != len(all) || bad != 0 {
		return ErrBadBackup
	}
	for _, c := range all {
		var raw []byte
		if q.QueryRow("SELECT body FROM "+checkpointBundleTable+" WHERE id=?", c.ID).Scan(&raw) != nil || checkpoint.Digest(raw) != c.FileDigest {
			return ErrBadBackup
		}
		snap, e := checkpoint.Decode(raw)
		if e != nil || snap.Head != c.HeadSHA || snap.Branch != c.Branch || snap.Worktree != c.Worktree || len(snap.Files) != c.FileCount {
			return ErrBadBackup
		}
		ss, e := loadSession(q, c.SessionID)
		if e != nil || ss.TaskID != c.TaskID || ss.ContractDigest != c.ContractDigest || ss.ProfileDigest != c.ProfileDigest {
			return ErrBadBackup
		}
		var run *model.Run
		for i := range st.Runs {
			if st.Runs[i].ID == c.RunID {
				run = &st.Runs[i]
			}
		}
		if run == nil || run.TaskID != c.TaskID || run.State != model.RunStopped || run.Role != c.Role || run.Usage == nil || run.Usage.Tokens.Total == nil {
			return ErrBadBackup
		}
		if c.State == "saved" && (ss.State != model.SessionIdle || ss.FileDigest != c.SessionDigest || ss.LastSHA != c.HeadSHA) {
			return ErrBadBackup
		}
		if c.ResumedRunID != nil {
			var sid string
			if q.QueryRow("SELECT session_id FROM session_runs WHERE run_id=?", *c.ResumedRunID).Scan(&sid) != nil || sid != c.SessionID {
				return ErrBadBackup
			}
		}
		if fn != nil {
			if e := fn(c, raw); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) materializeCheckpoints() error {
	tx, e := s.rdb.Begin()
	if e != nil {
		return e
	}
	e = verifyCheckpointBundle(tx, func(c model.Checkpoint, raw []byte) error { return s.WriteCheckpointFile(c, raw) })
	tx.Rollback()
	if e != nil {
		return e
	}
	return s.tx(func(tx *sql.Tx) error { _, e := tx.Exec("DROP TABLE IF EXISTS " + checkpointBundleTable); return e })
}
