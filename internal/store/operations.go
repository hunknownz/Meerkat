package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

// A task belongs to at most one unsettled dispatch. Foreign keys are deferred,
// because legacy state writes replace task rows inside the same transaction.
const migrationV3 = `
CREATE TABLE operations (id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE,
  request_digest TEXT NOT NULL, state TEXT NOT NULL, payload TEXT NOT NULL);
CREATE INDEX operations_state ON operations(state);
CREATE TABLE operation_members (operation_id TEXT NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
  task_id TEXT NOT NULL REFERENCES tasks(id) DEFERRABLE INITIALLY DEFERRED, state TEXT NOT NULL,
  PRIMARY KEY (operation_id, task_id));
CREATE UNIQUE INDEX operation_task_owner ON operation_members(task_id) WHERE state IN ('queued', 'running');
PRAGMA user_version = 3;
`

var operationTables = []string{"operations", "operation_members"}
var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
var operationSHARE = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

const MaxActiveOperations = 128

// validateOperationRows rejects mismatched indexed columns and payloads rather
// than using a corrupt queue or treating an unknown result as completed.
func loadOperation(q querier, field, value string) (model.Operation, error) {
	var o model.Operation
	var id, rid, digest, state string
	var payload []byte
	err := q.QueryRow("SELECT id, request_id, request_digest, state, payload FROM operations WHERE "+field+" = ?", value).
		Scan(&id, &rid, &digest, &state, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	if err != nil || json.Unmarshal(payload, &o) != nil {
		return o, fmt.Errorf("store: operation unreadable")
	}
	if o.ID != id || o.RequestID != rid || o.RequestDigest != digest || o.State != state || validOperation(o) != nil {
		return model.Operation{}, fmt.Errorf("store: operation corrupt")
	}
	rows, err := q.Query("SELECT task_id, state FROM operation_members WHERE operation_id = ?", o.ID)
	if err != nil {
		return model.Operation{}, fmt.Errorf("store: operation members unreadable")
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var taskID, state string
		if rows.Scan(&taskID, &state) != nil {
			return model.Operation{}, fmt.Errorf("store: operation members unreadable")
		}
		i := slices.IndexFunc(o.Tasks, func(m model.OperationTask) bool { return m.TaskID == taskID && m.State == state })
		if i < 0 {
			return model.Operation{}, fmt.Errorf("store: operation members corrupt")
		}
		n++
	}
	if rows.Err() != nil || n != len(o.Tasks) {
		return model.Operation{}, fmt.Errorf("store: operation members corrupt")
	}
	return o, nil
}

func validOperation(o model.Operation) error {
	if o.SchemaVersion != 1 || !uuidRE.MatchString(o.ID) || !uuidRE.MatchString(o.RequestID) || !digestRE.MatchString(o.RequestDigest) ||
		!slices.Contains([]string{model.OperationQueued, model.OperationRunning, model.OperationCompleted, model.OperationUnknown}, o.State) ||
		!slices.Contains([]string{"workflow", "delegate"}, o.Mode) || len(o.Tasks) < 1 || len(o.Tasks) > 50 ||
		o.FrozenFixRounds < 0 || o.FrozenFixRounds > model.MaxFixRoundsLimit {
		return model.Invalidf("invalid operation")
	}
	seen := map[string]bool{}
	for _, at := range []string{o.CreatedAt, o.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			return model.Invalidf("invalid operation time")
		}
	}
	for _, at := range []*string{o.StartedAt, o.EndedAt} {
		if at != nil {
			if _, err := time.Parse(time.RFC3339Nano, *at); err != nil {
				return model.Invalidf("invalid operation time")
			}
		}
	}
	for _, m := range o.Tasks {
		if !uuidRE.MatchString(m.TaskID) || seen[m.TaskID] || !digestRE.MatchString(m.ContractDigest) ||
			!slices.Contains([]string{model.MemberQueued, model.MemberRunning, model.MemberCompleted, model.MemberUnknown}, m.State) || !slices.Contains(model.TaskStates, m.TaskState) ||
			m.CandidateSHA != nil && !operationSHARE.MatchString(*m.CandidateSHA) || m.ResumeRole != nil && !model.IsRole(*m.ResumeRole) {
			return model.Invalidf("invalid operation member")
		}
		seen[m.TaskID] = true
		for _, at := range []*string{m.StartedAt, m.EndedAt} {
			if at != nil {
				if _, err := time.Parse(time.RFC3339Nano, *at); err != nil {
					return model.Invalidf("invalid operation member time")
				}
			}
		}
		if m.State == model.MemberQueued && (m.StartedAt != nil || m.EndedAt != nil) ||
			m.State == model.MemberRunning && (m.StartedAt == nil || m.EndedAt != nil) ||
			(m.State == model.MemberCompleted || m.State == model.MemberUnknown) && m.EndedAt == nil {
			return model.Invalidf("inconsistent operation member time")
		}
		if o.State == model.OperationQueued && (m.State != model.MemberQueued || m.StartedAt != nil) || o.State == model.OperationCompleted && m.State != model.MemberCompleted ||
			o.State == model.OperationUnknown && (m.State == model.MemberQueued || m.State == model.MemberRunning) {
			return model.Invalidf("inconsistent operation state")
		}
	}
	if o.State == model.OperationQueued && (o.StartedAt != nil || o.EndedAt != nil) ||
		o.State == model.OperationRunning && (o.StartedAt == nil || o.EndedAt != nil) ||
		model.OperationSettled(o.State) && (o.StartedAt == nil || o.EndedAt == nil) {
		return model.Invalidf("inconsistent operation time")
	}
	return nil
}

// validateOperations checks queue payload/index agreement in a backup before
// restoring it. SQLite structural integrity alone cannot validate JSON records.
func validateOperations(q querier) error {
	rows, err := q.Query("SELECT id FROM operations ORDER BY rowid")
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
	for _, id := range ids {
		if _, err := loadOperation(q, "id", id); err != nil {
			return badBackup("operation records are corrupt")
		}
	}
	return nil
}

func putOperation(tx *sql.Tx, o model.Operation) error {
	if err := validOperation(o); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO operations (id, request_id, request_digest, state, payload) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET state = excluded.state, payload = excluded.payload`, o.ID, o.RequestID, o.RequestDigest, o.State, mustJSON(o)); err != nil {
		return fmt.Errorf("%w: operation identity", ErrConflict)
	}
	if _, err := tx.Exec("DELETE FROM operation_members WHERE operation_id = ?", o.ID); err != nil {
		return fmt.Errorf("store: write operation failed")
	}
	for _, m := range o.Tasks {
		if _, err := tx.Exec("INSERT INTO operation_members (operation_id, task_id, state) VALUES (?, ?, ?)", o.ID, m.TaskID, m.State); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: task already queued or running", ErrConflict)
			}
			return fmt.Errorf("store: write operation member failed")
		}
	}
	return nil
}

// Operation and OperationByRequest read one consistent dispatch, including its
// membership. The caller is responsible for using Public before exposing it.
func (s *Store) Operation(id string) (model.Operation, error) { return s.operationRead("id", id) }
func (s *Store) OperationByRequest(id string) (model.Operation, error) {
	return s.operationRead("request_id", id)
}
func (s *Store) operationRead(field, value string) (model.Operation, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return model.Operation{}, fmt.Errorf("store: operation read failed")
	}
	defer tx.Rollback()
	return loadOperation(tx, field, value)
}

func activeOperations(q querier) ([]model.Operation, error) {
	rows, err := q.Query("SELECT id FROM operations WHERE state IN ('queued', 'running') ORDER BY rowid")
	if err != nil {
		return nil, fmt.Errorf("store: queue unreadable")
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, fmt.Errorf("store: queue unreadable")
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("store: queue unreadable")
	}
	out := []model.Operation{}
	for _, id := range ids {
		o, err := loadOperation(q, "id", id)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Store) ActiveOperations() ([]model.Operation, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: queue read failed")
	}
	defer tx.Rollback()
	return activeOperations(tx)
}

// EnqueueOwned commits the operation, task claims and state together. fn must
// only validate/change in-memory state: no Git, network or model calls in SQL.
func (s *Store) EnqueueOwned(token string, o model.Operation, fn func(*model.State) error) (model.Operation, bool, error) {
	duplicate := false
	res := o
	err := s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		old, err := loadOperation(tx, "request_id", o.RequestID)
		if err == nil {
			if old.RequestDigest != o.RequestDigest {
				return fmt.Errorf("%w: requestId has different input", ErrConflict)
			}
			res, duplicate = old, true
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var n int
		if tx.QueryRow("SELECT count(*) FROM operations WHERE state IN ('queued', 'running')").Scan(&n) != nil {
			return fmt.Errorf("store: queue unreadable")
		}
		if n >= MaxActiveOperations {
			return fmt.Errorf("%w: queue full", ErrConflict)
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
		return putOperation(tx, o)
	})
	return res, duplicate, err
}

// UpdateOperationOwned atomically records task state and its operation result.
// Immutable identity and membership cannot be changed by a callback.
func (s *Store) UpdateOperationOwned(token, id string, fn func(*model.Operation, *model.State) error) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		o, err := loadOperation(tx, "id", id)
		if err != nil {
			return err
		}
		before := o
		before.Tasks = slices.Clone(o.Tasks)
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := fn(&o, st); err != nil {
			return err
		}
		if o.ID != before.ID || o.RequestID != before.RequestID || o.RequestDigest != before.RequestDigest || o.Mode != before.Mode ||
			o.CreatedAt != before.CreatedAt || o.FrozenFixRounds != before.FrozenFixRounds || len(o.Tasks) != len(before.Tasks) {
			return ErrConflict
		}
		for i := range o.Tasks {
			if o.Tasks[i].TaskID != before.Tasks[i].TaskID || o.Tasks[i].ContractDigest != before.Tasks[i].ContractDigest {
				return ErrConflict
			}
		}
		if err := writeState(tx, st); err != nil {
			return err
		}
		return putOperation(tx, o)
	})
}
