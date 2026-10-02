// Package store persists Meerkat workflow state in a private SQLite database.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	_ "modernc.org/sqlite"
)

var (
	ErrConflict   = errors.New("store: conflict")
	ErrLeaseHeld  = errors.New("store: controller lease held")
	ErrLeaseLost  = errors.New("store: controller lease lost")
	ErrUnknownRun = errors.New("store: unknown run")
	ErrStopLimit  = errors.New("store: too many pending stop requests")
	ErrNotFound   = errors.New("store: not found")
	ErrUnsafeDir  = errors.New("store: unsafe data directory")
	ErrBadBackup  = errors.New("store: invalid backup")
)

const dbName = "meerkat.db"

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var schemaTables = []string{"projects", "contexts", "profiles", "tasks", "task_dependencies", "runs", "run_events", "deliveries",
	"reviews", "usage", "settings", "controller_lease", "stop_receipts", "worktree_claims", "imports", "legacy_history", "issue_receipts"}

var migrationV1 = `
CREATE TABLE projects (id TEXT PRIMARY KEY, payload TEXT NOT NULL);
CREATE TABLE contexts (id TEXT NOT NULL, version INTEGER NOT NULL, project_id TEXT NOT NULL REFERENCES projects(id),
  digest TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY (id, version));
CREATE INDEX contexts_project ON contexts(project_id);
CREATE TABLE profiles (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), payload TEXT NOT NULL);
CREATE INDEX profiles_project ON profiles(project_id);
CREATE TABLE tasks (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), worktree TEXT NOT NULL,
  state TEXT NOT NULL, context_id TEXT NOT NULL, context_version INTEGER NOT NULL, payload TEXT NOT NULL,
  FOREIGN KEY (context_id, context_version) REFERENCES contexts(id, version));
CREATE INDEX tasks_project ON tasks(project_id);
CREATE INDEX tasks_context ON tasks(context_id, context_version);
CREATE UNIQUE INDEX tasks_active_worktree ON tasks(worktree) WHERE state IN (` + quoteList(model.ActiveTaskStates) + `);
CREATE TABLE task_dependencies (task_id TEXT NOT NULL REFERENCES tasks(id), depends_on TEXT NOT NULL REFERENCES tasks(id),
  ord INTEGER NOT NULL, PRIMARY KEY (task_id, depends_on));
CREATE INDEX task_dependencies_dep ON task_dependencies(depends_on);
CREATE TABLE runs (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), state TEXT NOT NULL,
  lease_token TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL);
CREATE INDEX runs_task ON runs(task_id);
CREATE TABLE run_events (run_id TEXT NOT NULL REFERENCES runs(id), seq INTEGER NOT NULL, payload TEXT NOT NULL,
  PRIMARY KEY (run_id, seq));
CREATE TABLE deliveries (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), payload TEXT NOT NULL);
CREATE INDEX deliveries_task ON deliveries(task_id);
CREATE TABLE reviews (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), run_id TEXT NOT NULL REFERENCES runs(id),
  payload TEXT NOT NULL);
CREATE INDEX reviews_task ON reviews(task_id);
CREATE INDEX reviews_run ON reviews(run_id);
CREATE TABLE usage (run_id TEXT PRIMARY KEY REFERENCES runs(id), payload TEXT NOT NULL);
CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK (id = 1), payload TEXT NOT NULL);
CREATE TABLE controller_lease (id INTEGER PRIMARY KEY CHECK (id = 1), token TEXT NOT NULL, pid INTEGER NOT NULL,
  host TEXT NOT NULL, acquired_at TEXT NOT NULL, heartbeat_at TEXT NOT NULL);
CREATE TABLE stop_receipts (request_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, state TEXT NOT NULL, created_at TEXT NOT NULL,
  processed_at TEXT, outcome TEXT);
CREATE INDEX stop_receipts_state ON stop_receipts(state, created_at);
CREATE TABLE worktree_claims (worktree TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), claimed_at TEXT NOT NULL,
  lease_token TEXT NOT NULL);
CREATE TABLE imports (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, payload TEXT NOT NULL);
CREATE TABLE legacy_history (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, payload TEXT NOT NULL);
CREATE TABLE issue_receipts (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, payload TEXT NOT NULL);
PRAGMA user_version = 1;
`

// Schema versions this binary can open. schemaVersion is the version a fresh or migrated store ends at.
const (
	schemaV1      = 1
	schemaV2      = 2
	schemaVersion = schemaV2
)

// Settled delegate candidates (core.OriginDelegate / core.DelegateCandidate). They stay in first_delivery for
// history, but no longer hold their worktree. The store cannot import core, so the values are mirrored here and
// exercised end to end by the core delegate tests.
const (
	delegateOrigin    = "native_delegate"
	delegateCandidate = "delegate_candidate"
)

// activeWorktreePredicate is the partial-index predicate of tasks_active_worktree since v2: every active state
// holds the worktree except a first_delivery row whose payload is a valid JSON object with origin native_delegate
// and stateReason delegate_candidate. Malformed payloads and missing or non-matching values are never exempt.
var activeWorktreePredicate = `state IN (` + quoteList(model.ActiveTaskStates) + `) AND NOT (state = '` + model.TaskFirstDelivery + `'
  AND CASE WHEN json_valid(payload) AND json_type(payload) = 'object' THEN
    COALESCE(json_extract(payload, '$.origin') = '` + delegateOrigin + `', 0)
    AND COALESCE(json_extract(payload, '$.stateReason') = '` + delegateCandidate + `', 0)
  ELSE 0 END)`

// migrationV2 releases the worktree of settled delegate candidates. Rows are not touched; only the index changes.
var migrationV2 = `
DROP INDEX tasks_active_worktree;
CREATE UNIQUE INDEX tasks_active_worktree ON tasks(worktree) WHERE ` + activeWorktreePredicate + `;
PRAGMA user_version = 2;
`

func quoteList(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = "'" + s + "'"
	}
	return strings.Join(q, ",")
}

// Store is a handle on one private data directory.
type Store struct {
	dir  string
	db   *sql.DB // writer pool: every transaction is BEGIN IMMEDIATE
	rdb  *sql.DB // reader pool: query_only, BEGIN DEFERRED snapshots that never take the writer lock
	host string
}

type dsnMode int

const (
	dsnWriter dsnMode = iota
	dsnReader
	dsnReadOnlyFile
	dsnBackupFile // single-file rollback-journal database (never switched to WAL)
)

// dsn builds a SQLite URI. The path is percent-encoded through net/url so that characters such as
// '?', '#', '%' and spaces in the data directory cannot be mistaken for URI query or fragment syntax.
func dsn(path string, mode dsnMode) string {
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"} {
		q.Add("_pragma", p)
	}
	switch mode {
	case dsnReadOnlyFile:
		q.Set("mode", "ro")
	case dsnBackupFile:
		q.Set("_txlock", "immediate")
	case dsnReader:
		q.Add("_pragma", "query_only(1)")
		q.Set("_txlock", "deferred")
	default:
		q.Add("_pragma", "journal_mode(WAL)")
		q.Set("_txlock", "immediate")
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	return u.String()
}

func checkDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.Mkdir(dir, 0o700); err != nil {
			return ErrUnsafeDir
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		return ErrUnsafeDir
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return ErrUnsafeDir
	}
	return nil
}

func checkFile(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return ErrUnsafeDir
		}
		return f.Close()
	}
	if err != nil || !fi.Mode().IsRegular() {
		return ErrUnsafeDir
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return ErrUnsafeDir
	}
	if fi.Mode().Perm() != 0o600 {
		return os.Chmod(path, 0o600)
	}
	return nil
}

// Open opens (creating if needed) the store in dir.
func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, ErrUnsafeDir
	}
	if err := checkDir(abs); err != nil {
		return nil, err
	}
	path := filepath.Join(abs, dbName)
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Lstat(p); p == path || err == nil {
			if err := checkFile(p); err != nil {
				return nil, err
			}
		}
	}
	db, err := sql.Open("sqlite", dsn(path, dsnWriter))
	if err != nil {
		return nil, fmt.Errorf("store: open database")
	}
	host, _ := os.Hostname()
	s := &Store{dir: abs, db: db, host: host}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	rdb, err := sql.Open("sqlite", dsn(path, dsnReader))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open database")
	}
	s.rdb = rdb
	return s, nil
}

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: migrate begin")
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("store: read schema version")
	}
	if v < 0 || v > schemaVersion {
		return fmt.Errorf("store: unsupported schema version %d", v)
	}
	if v == schemaVersion {
		return nil
	}
	if v < schemaV1 {
		if _, err := tx.Exec(migrationV1); err != nil {
			return fmt.Errorf("store: migration v1 failed")
		}
	}
	if v < schemaV2 {
		if _, err := tx.Exec(migrationV2); err != nil {
			return fmt.Errorf("store: migration v2 failed")
		}
	}
	return tx.Commit()
}

// DataDir returns the absolute data directory.
func (s *Store) DataDir() string { return s.dir }

// Close closes the database.
func (s *Store) Close() error {
	var rerr error
	if s.rdb != nil {
		rerr = s.rdb.Close()
	}
	if err := s.db.Close(); err != nil {
		return err
	}
	return rerr
}

type querier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}

func scanPayloads(q querier, query string, fn func(payload []byte, extra ...any) error, extra ...any) error {
	rows, err := q.Query(query)
	if err != nil {
		return fmt.Errorf("store: query failed")
	}
	defer rows.Close()
	for rows.Next() {
		var p []byte
		dest := append([]any{&p}, extra...)
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("store: scan failed")
		}
		if err := fn(p, extra...); err != nil {
			return err
		}
	}
	if rows.Err() != nil {
		return fmt.Errorf("store: query failed")
	}
	return nil
}

func into[T any](dst *[]T) func([]byte, ...any) error {
	return func(p []byte, _ ...any) error {
		var v T
		if err := json.Unmarshal(p, &v); err != nil {
			return fmt.Errorf("store: corrupt record")
		}
		*dst = append(*dst, v)
		return nil
	}
}

func readState(q querier) (*model.State, error) {
	st := model.EmptyState()
	if err := scanPayloads(q, "SELECT payload FROM projects ORDER BY rowid", into(&st.Projects)); err != nil {
		return nil, err
	}
	if err := scanPayloads(q, "SELECT payload FROM contexts ORDER BY rowid", into(&st.Contexts)); err != nil {
		return nil, err
	}
	if err := scanPayloads(q, "SELECT payload FROM profiles ORDER BY rowid", into(&st.Profiles)); err != nil {
		return nil, err
	}
	if err := scanPayloads(q, "SELECT payload FROM tasks ORDER BY rowid", into(&st.Tasks)); err != nil {
		return nil, err
	}
	deps := map[string][]string{}
	var tid string
	if err := scanPayloads(q, "SELECT depends_on, task_id FROM task_dependencies ORDER BY task_id, ord", func(p []byte, _ ...any) error {
		deps[tid] = append(deps[tid], string(p))
		return nil
	}, &tid); err != nil {
		return nil, err
	}
	for i := range st.Tasks {
		st.Tasks[i].Dependencies = append([]string{}, deps[st.Tasks[i].ID]...)
	}
	var token string
	if err := scanPayloads(q, "SELECT payload, lease_token FROM runs ORDER BY rowid", func(p []byte, _ ...any) error {
		var r model.Run
		if err := json.Unmarshal(p, &r); err != nil {
			return fmt.Errorf("store: corrupt record")
		}
		r.LeaseToken, r.Events = token, []model.RunEvent{}
		st.Runs = append(st.Runs, r)
		return nil
	}, &token); err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, r := range st.Runs {
		idx[r.ID] = i
	}
	var rid string
	if err := scanPayloads(q, "SELECT payload, run_id FROM run_events ORDER BY run_id, seq", func(p []byte, _ ...any) error {
		var e model.RunEvent
		if err := json.Unmarshal(p, &e); err != nil {
			return fmt.Errorf("store: corrupt record")
		}
		st.Runs[idx[rid]].Events = append(st.Runs[idx[rid]].Events, e)
		return nil
	}, &rid); err != nil {
		return nil, err
	}
	if err := scanPayloads(q, "SELECT payload, run_id FROM usage", func(p []byte, _ ...any) error {
		u := &model.Usage{}
		if err := json.Unmarshal(p, u); err != nil {
			return fmt.Errorf("store: corrupt record")
		}
		st.Runs[idx[rid]].Usage = u
		return nil
	}, &rid); err != nil {
		return nil, err
	}
	if err := scanPayloads(q, "SELECT payload FROM deliveries ORDER BY rowid", into(&st.Deliveries)); err != nil {
		return nil, err
	}
	if err := scanPayloads(q, "SELECT payload FROM reviews ORDER BY rowid", into(&st.Reviews)); err != nil {
		return nil, err
	}
	return st, nil
}

// Read returns a consistent snapshot of the complete state. It uses a deferred transaction on the
// reader pool, so under WAL it neither takes nor waits for the writer lock and observes the last
// committed state across all tables.
func (s *Store) Read() (*model.State, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: begin failed")
	}
	defer tx.Rollback()
	return readState(tx)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeState(tx *sql.Tx, st *model.State) error {
	ex := func(q string, args ...any) error {
		if _, err := tx.Exec(q, args...); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: unique constraint", ErrConflict)
			}
			return fmt.Errorf("store: write failed")
		}
		return nil
	}
	if err := ex("PRAGMA defer_foreign_keys = ON"); err != nil {
		return err
	}
	for _, t := range []string{"worktree_claims", "usage", "run_events", "reviews", "deliveries", "runs", "task_dependencies", "tasks", "profiles", "projects"} {
		if err := ex("DELETE FROM " + t); err != nil {
			return err
		}
	}
	for _, p := range st.Projects {
		if err := ex("INSERT INTO projects (id, payload) VALUES (?, ?)", p.ID, mustJSON(p)); err != nil {
			return err
		}
	}
	seen := map[[2]any]bool{}
	for _, c := range st.Contexts {
		key := [2]any{c.ID, c.Version}
		seen[key] = true
		payload := mustJSON(c)
		var old string
		err := tx.QueryRow("SELECT payload FROM contexts WHERE id = ? AND version = ?", c.ID, c.Version).Scan(&old)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := ex("INSERT INTO contexts (id, version, project_id, digest, payload) VALUES (?, ?, ?, ?, ?)", c.ID, c.Version, c.ProjectID, c.Digest, payload); err != nil {
				return err
			}
		case err != nil:
			return fmt.Errorf("store: read failed")
		case old != payload:
			return fmt.Errorf("%w: context versions are immutable", ErrConflict)
		}
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM contexts").Scan(&n); err != nil || n != len(seen) {
		return fmt.Errorf("%w: context versions cannot be removed", ErrConflict)
	}
	for _, p := range st.Profiles {
		if err := ex("INSERT INTO profiles (id, project_id, payload) VALUES (?, ?, ?)", p.ID, p.ProjectID, mustJSON(p)); err != nil {
			return err
		}
	}
	for _, t := range st.Tasks {
		deps := t.Dependencies
		t.Dependencies = nil
		if err := ex("INSERT INTO tasks (id, project_id, worktree, state, context_id, context_version, payload) VALUES (?, ?, ?, ?, ?, ?, ?)",
			t.ID, t.ProjectID, t.Worktree, t.State, t.ContextRef.ID, t.ContextRef.Version, mustJSON(t)); err != nil {
			return err
		}
		for i, d := range deps {
			if err := ex("INSERT INTO task_dependencies (task_id, depends_on, ord) VALUES (?, ?, ?)", t.ID, d, i); err != nil {
				return err
			}
		}
	}
	for _, r := range st.Runs {
		events, usage, token := r.Events, r.Usage, r.LeaseToken
		r.Events, r.Usage = nil, nil
		if err := ex("INSERT INTO runs (id, task_id, state, lease_token, payload) VALUES (?, ?, ?, ?, ?)", r.ID, r.TaskID, r.State, token, mustJSON(r)); err != nil {
			return err
		}
		if len(events) > model.MaxRunEvents {
			events = events[len(events)-model.MaxRunEvents:]
		}
		for i, e := range events {
			if err := ex("INSERT INTO run_events (run_id, seq, payload) VALUES (?, ?, ?)", r.ID, i, mustJSON(e)); err != nil {
				return err
			}
		}
		if usage != nil {
			if err := ex("INSERT INTO usage (run_id, payload) VALUES (?, ?)", r.ID, mustJSON(usage)); err != nil {
				return err
			}
		}
	}
	for _, d := range st.Deliveries {
		if err := ex("INSERT INTO deliveries (id, task_id, payload) VALUES (?, ?, ?)", d.ID, d.TaskID, mustJSON(d)); err != nil {
			return err
		}
	}
	for _, r := range st.Reviews {
		if err := ex("INSERT INTO reviews (id, task_id, run_id, payload) VALUES (?, ?, ?, ?)", r.ID, r.TaskID, r.RunID, mustJSON(r)); err != nil {
			return err
		}
	}
	return checkFKs(tx)
}

func checkFKs(q querier) error {
	rows, err := q.Query("PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("store: foreign key check failed")
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("%w: foreign key violation", model.ErrInvalid)
	}
	return nil
}

// Update runs fn against the current state inside one immediate write transaction and persists the result atomically.
func (s *Store) Update(fn func(*model.State) error) error {
	return s.tx(func(tx *sql.Tx) error {
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := fn(st); err != nil {
			return err
		}
		return writeState(tx, st)
	})
}

func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin failed")
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit failed")
	}
	return nil
}

// GetSettings returns stored settings or defaults.
func (s *Store) GetSettings() (model.Settings, error) {
	set := model.DefaultSettings()
	var p []byte
	err := s.db.QueryRow("SELECT payload FROM settings WHERE id = 1").Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return set, nil
	}
	if err != nil || json.Unmarshal(p, &set) != nil {
		return set, fmt.Errorf("store: read settings failed")
	}
	return set, nil
}

// SetSettings replaces the settings.
func (s *Store) SetSettings(set model.Settings) error {
	if set.MaxConcurrency < model.MinConcurrency || set.MaxConcurrency > model.MaxConcurrency || set.MaxFixRounds < 0 || set.MaxFixRounds > model.MaxFixRoundsLimit {
		return model.Invalidf("settings out of range")
	}
	if set.DefaultProfiles == nil {
		set.DefaultProfiles = map[string]map[string]string{}
	}
	_, err := s.db.Exec("INSERT INTO settings (id, payload) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET payload = excluded.payload", mustJSON(set))
	if err != nil {
		return fmt.Errorf("store: write settings failed")
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("store: token generation failed")
	}
	return hex.EncodeToString(b), nil
}

// AcquireLease takes the controller lease unless it is held by a live local process or a process on another host.
func (s *Store) AcquireLease() (model.ControllerLease, error) {
	var l model.ControllerLease
	err := s.tx(func(tx *sql.Tx) error {
		var pid int
		var host string
		err := tx.QueryRow("SELECT pid, host FROM controller_lease WHERE id = 1").Scan(&pid, &host)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: read lease failed")
		}
		if err == nil && (host != s.host || s.host == "" || pidAlive(pid)) {
			return ErrLeaseHeld
		}
		tok, err := newToken()
		if err != nil {
			return err
		}
		t := now()
		l = model.ControllerLease{Token: tok, PID: os.Getpid(), Host: s.host, AcquiredAt: t, HeartbeatAt: t}
		if _, err := tx.Exec("INSERT OR REPLACE INTO controller_lease (id, token, pid, host, acquired_at, heartbeat_at) VALUES (1, ?, ?, ?, ?, ?)",
			l.Token, l.PID, l.Host, l.AcquiredAt, l.HeartbeatAt); err != nil {
			return fmt.Errorf("store: write lease failed")
		}
		return nil
	})
	return l, err
}

// RenewLease refreshes the heartbeat if token still holds the lease.
func (s *Store) RenewLease(token string) error {
	res, err := s.db.Exec("UPDATE controller_lease SET heartbeat_at = ? WHERE id = 1 AND token = ? AND token != ''", now(), token)
	if err != nil {
		return fmt.Errorf("store: renew lease failed")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrLeaseLost
	}
	return nil
}

// ReleaseLease drops the lease if token holds it.
func (s *Store) ReleaseLease(token string) error {
	res, err := s.db.Exec("DELETE FROM controller_lease WHERE id = 1 AND token = ? AND token != ''", token)
	if err != nil {
		return fmt.Errorf("store: release lease failed")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrLeaseLost
	}
	return nil
}

// LeaseFacts reports the recorded lease row without the token.
func (s *Store) LeaseFacts() (model.LeaseFacts, error) {
	var f model.LeaseFacts
	err := s.db.QueryRow("SELECT pid, host, acquired_at, heartbeat_at FROM controller_lease WHERE id = 1").Scan(&f.PID, &f.Host, &f.AcquiredAt, &f.HeartbeatAt)
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("store: read lease failed")
	}
	f.Present = true
	f.Stale = f.Host == s.host && s.host != "" && !pidAlive(f.PID)
	return f, nil
}

// RequestStop records an idempotent stop request for a known run.
func (s *Store) RequestStop(runID, requestID string) (model.StopReceipt, error) {
	var rc model.StopReceipt
	if !uuidRE.MatchString(requestID) {
		return rc, model.Invalidf("stop request id must be a UUID")
	}
	requestID = strings.ToLower(requestID)
	err := s.tx(func(tx *sql.Tx) error {
		var oldRun string
		err := tx.QueryRow("SELECT run_id, state, created_at, processed_at, outcome FROM stop_receipts WHERE request_id = ?", requestID).
			Scan(&oldRun, &rc.State, &rc.CreatedAt, &rc.ProcessedAt, &rc.Outcome)
		if err == nil {
			if oldRun != runID {
				return fmt.Errorf("%w: stop request id reused", ErrConflict)
			}
			rc.RequestID, rc.RunID, rc.Accepted, rc.Duplicate = requestID, runID, true, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: read stop failed")
		}
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM runs WHERE id = ?", runID).Scan(&n); err != nil {
			return fmt.Errorf("store: read run failed")
		}
		if n == 0 {
			return ErrUnknownRun
		}
		if err := tx.QueryRow("SELECT count(*) FROM stop_receipts WHERE state = ?", model.StopPending).Scan(&n); err != nil {
			return fmt.Errorf("store: read stop failed")
		}
		if n >= model.MaxStopRequests {
			return ErrStopLimit
		}
		rc = model.StopReceipt{RequestID: requestID, RunID: runID, Accepted: true, State: model.StopPending, CreatedAt: now()}
		if _, err := tx.Exec("INSERT INTO stop_receipts (request_id, run_id, state, created_at) VALUES (?, ?, ?, ?)", requestID, runID, rc.State, rc.CreatedAt); err != nil {
			return fmt.Errorf("store: write stop failed")
		}
		if _, err := tx.Exec(`DELETE FROM stop_receipts WHERE state != ? AND request_id NOT IN
			(SELECT request_id FROM stop_receipts ORDER BY created_at DESC LIMIT ?)`, model.StopPending, model.MaxStopReceipts); err != nil {
			return fmt.Errorf("store: prune stop failed")
		}
		return nil
	})
	return rc, err
}

// StopRequests lists pending stop requests oldest first.
func (s *Store) StopRequests() ([]model.StopRequest, error) {
	rows, err := s.db.Query("SELECT request_id, run_id, created_at FROM stop_receipts WHERE state = ? ORDER BY created_at, rowid", model.StopPending)
	if err != nil {
		return nil, fmt.Errorf("store: read stops failed")
	}
	defer rows.Close()
	out := []model.StopRequest{}
	for rows.Next() {
		r := model.StopRequest{Type: "stop"}
		if err := rows.Scan(&r.RequestID, &r.RunID, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: read stops failed")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FinishStop marks a pending stop request processed with the run's actual outcome.
func (s *Store) FinishStop(requestID, outcome string) error {
	res, err := s.db.Exec("UPDATE stop_receipts SET state = ?, processed_at = ?, outcome = ? WHERE request_id = ? AND state = ?",
		model.StopProcessed, now(), outcome, strings.ToLower(requestID), model.StopPending)
	if err != nil {
		return fmt.Errorf("store: finish stop failed")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
