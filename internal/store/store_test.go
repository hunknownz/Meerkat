package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func seed(st *model.State) {
	ctx := model.Context{ID: "c1", ProjectID: "p1", Version: 1, Digest: "d1", Text: "t", Sources: []model.ContextSource{}, CreatedAt: "x"}
	st.Projects = append(st.Projects, model.Project{ID: "p1", Name: "P", Repositories: []string{"/r"}})
	st.Contexts = append(st.Contexts, ctx)
	st.Profiles = append(st.Profiles, model.Profile{ID: "pr1", ProjectID: "p1", Role: "developer"})
	mk := func(id, wt, state string, deps ...string) model.Task {
		return model.Task{ID: id, ProjectID: "p1", Worktree: wt, State: state, ContextRef: ctx.Ref(), Dependencies: deps}
	}
	st.Tasks = append(st.Tasks, mk("t1", "/w1", model.TaskImplementing), mk("t2", "/w1", model.TaskQueued, "t1"), mk("t3", "/w1", model.TaskQueued))
	pid := 1
	in := int64(5)
	st.Runs = append(st.Runs, model.Run{ID: "r1", TaskID: "t1", Role: "developer", State: model.RunRunning, PID: &pid, LeaseToken: "lt",
		Events: []model.RunEvent{{Type: "a", Summary: "s"}}, Usage: &model.Usage{Tokens: model.TokenCounts{Input: &in}, UsageCompleteness: "partial"}})
	st.Deliveries = append(st.Deliveries, model.Delivery{ID: "d1", TaskID: "t1", ContextRef: ctx.Ref(), RunIDs: []string{"r1"}})
	st.Reviews = append(st.Reviews, model.Review{ID: "v1", TaskID: "t1", RunID: "r1", Verdict: "pass"})
}

func TestOpenPermissionsAndRoundtrip(t *testing.T) {
	s, dir := openTemp(t)
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatal("dir perms")
	}
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{dbName, dbName + "-wal", dbName + "-shm"} {
		if fi, err := os.Stat(filepath.Join(dir, f)); err == nil && fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s perms %v", f, fi.Mode().Perm())
		}
	}
	st, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Tasks) != 3 || len(st.Tasks[1].Dependencies) != 1 || st.Tasks[1].Dependencies[0] != "t1" || len(st.Contexts) != 1 {
		t.Fatalf("tasks %+v", st.Tasks)
	}
	r := st.Runs[0]
	if r.LeaseToken != "lt" || len(r.Events) != 1 || r.Usage == nil || *r.Usage.Tokens.Input != 5 || *r.PID != 1 {
		t.Fatalf("run %+v", r)
	}
	if len(st.Deliveries) != 1 || len(st.Reviews) != 1 || len(st.Profiles) != 1 {
		t.Fatal("missing records")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(dir, link)
	if _, err := Open(link); !errors.Is(err, ErrUnsafeDir) {
		t.Fatal("symlink dir accepted")
	}
}

func TestFKRollbackAndImmutableContext(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	err := s.Update(func(st *model.State) error {
		st.Projects = append(st.Projects, model.Project{ID: "p2"})
		st.Runs = append(st.Runs, model.Run{ID: "r2", TaskID: "missing", State: model.RunStarting})
		return nil
	})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("want FK error, got %v", err)
	}
	st, _ := s.Read()
	if len(st.Projects) != 1 || len(st.Runs) != 1 {
		t.Fatal("partial write survived rollback")
	}
	if err := s.Update(func(st *model.State) error { st.Contexts[0].Digest = "changed"; return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("context mutation allowed: %v", err)
	}
	if err := s.Update(func(st *model.State) error { st.Contexts = nil; return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("context removal allowed: %v", err)
	}
	if err := s.Update(func(st *model.State) error {
		c := st.Contexts[0]
		c.Version, c.Digest = 2, "d2"
		st.Contexts = append(st.Contexts, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActiveWorktreeCollision(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	err := s.Update(func(st *model.State) error { st.Tasks[1].State = model.TaskChecking; return nil })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("two active tasks on one worktree: %v", err)
	}
}

func TestTwoStoreTransactionRace(t *testing.T) {
	a, dir := openTemp(t)
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		for _, s := range []*Store{a, b} {
			wg.Add(1)
			go func(s *Store) {
				defer wg.Done()
				errs <- s.Update(func(st *model.State) error {
					st.Projects = append(st.Projects, model.Project{ID: fmt.Sprintf("p%d", len(st.Projects))})
					return nil
				})
			}(s)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	st, _ := a.Read()
	if len(st.Projects) != 2*n {
		t.Fatalf("lost updates: %d", len(st.Projects))
	}
}

func TestLeaseFencing(t *testing.T) {
	a, dir := openTemp(t)
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	la, err := a.AcquireLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AcquireLease(); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("stole from live process: %v", err)
	}
	a.db.Exec("UPDATE controller_lease SET host = 'other-host', pid = 999999")
	if _, err := b.AcquireLease(); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("stole from unknown host: %v", err)
	}
	a.db.Exec("UPDATE controller_lease SET host = ?", a.host)
	if f, _ := a.LeaseFacts(); !f.Present || !f.Stale {
		t.Fatalf("facts %+v", f)
	}
	lb, err := b.AcquireLease()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RenewLease(la.Token); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("fenced token renewed")
	}
	if err := a.ReleaseLease(la.Token); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("fenced token released")
	}
	if err := b.RenewLease(lb.Token); err != nil {
		t.Fatal(err)
	}
	if err := b.ReleaseLease(lb.Token); err != nil {
		t.Fatal(err)
	}
}

func TestStopDuplicate(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	id := "123e4567-e89b-12d3-a456-426614174000"
	r1, err := s.RequestStop("r1", id)
	if err != nil || r1.Duplicate || r1.State != model.StopPending {
		t.Fatal(r1, err)
	}
	r2, err := s.RequestStop("r1", id)
	if err != nil || !r2.Duplicate || r2.CreatedAt != r1.CreatedAt {
		t.Fatal(r2, err)
	}
	if _, err := s.RequestStop("r9", "223e4567-e89b-12d3-a456-426614174000"); !errors.Is(err, ErrUnknownRun) {
		t.Fatal("unknown run accepted")
	}
	if _, err := s.RequestStop("r1", "not-a-uuid"); !errors.Is(err, model.ErrInvalid) {
		t.Fatal("bad id accepted")
	}
	if reqs, _ := s.StopRequests(); len(reqs) != 1 {
		t.Fatal(reqs)
	}
	for i := 1; i < model.MaxStopRequests; i++ {
		if _, err := s.RequestStop("r1", fmt.Sprintf("00000000-0000-0000-0000-%012d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RequestStop("r1", "ffffffff-0000-0000-0000-000000000000"); !errors.Is(err, ErrStopLimit) {
		t.Fatal("cap not enforced")
	}
	if err := s.FinishStop(id, model.RunStopped); err != nil {
		t.Fatal(err)
	}
	r3, _ := s.RequestStop("r1", id)
	if r3.State != model.StopProcessed || r3.Outcome == nil || *r3.Outcome != model.RunStopped {
		t.Fatal(r3)
	}
}

func TestBackupRestoreAfterWAL(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSettings(model.Settings{MaxConcurrency: 3, MaxFixRounds: 1}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(dest); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(dest); err == nil {
		t.Fatal("overwrote backup")
	}
	rdir := filepath.Join(t.TempDir(), "restore")
	os.Mkdir(rdir, 0o700)
	data, _ := os.ReadFile(dest)
	os.WriteFile(filepath.Join(rdir, dbName), data, 0o600)
	r, err := Open(rdir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	st, err := r.Read()
	if err != nil || len(st.Tasks) != 3 || len(st.Runs[0].Events) != 1 {
		t.Fatal("restore mismatch", err)
	}
	if set, _ := r.GetSettings(); set.MaxConcurrency != 3 {
		t.Fatal("settings not restored")
	}
	bad := filepath.Join(t.TempDir(), "bad.db")
	os.WriteFile(bad, []byte("garbage"), 0o600)
	if ValidateBackup(bad) == nil {
		t.Fatal("garbage validated")
	}
}

func TestOpenDataDirWithURISpecialChars(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my data ?x=1 #frag %20")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, dbName)); err != nil || fi.Size() == 0 {
		t.Fatalf("database not created inside data dir: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if e.Name() != filepath.Base(dir) {
			t.Fatalf("stray file created outside data dir: %q", e.Name())
		}
	}
	st, err := s.Read()
	if err != nil || len(st.Tasks) != 3 {
		t.Fatalf("read back: %v", err)
	}
	if err := s.Backup(filepath.Join(dir, "b ?#.db")); err != nil {
		t.Fatal(err)
	}
}

func TestReadSnapshotNotBlockedByWriter(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.tx(func(tx *sql.Tx) error {
			st, err := readState(tx)
			if err != nil {
				return err
			}
			st.Projects[0].Name = "CHANGED"
			st.Tasks = st.Tasks[:2]
			st.Tasks[1].Dependencies = nil
			if err := writeState(tx, st); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := s.Read()
			if err != nil {
				errs <- err
				return
			}
			if st.Projects[0].Name != "P" || len(st.Tasks) != 3 || len(st.Tasks[1].Dependencies) != 1 {
				errs <- fmt.Errorf("reader saw uncommitted state")
			}
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("reads blocked behind writer")
	}
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st, err := s.Read()
	if err != nil || st.Projects[0].Name != "CHANGED" || len(st.Tasks) != 2 {
		t.Fatalf("committed state not visible: %v", err)
	}
}

// v1Fixture creates a real schema-v1 database file at path (as written by earlier releases) holding a settled
// delegate candidate, a regular managed first_delivery task, a run with events and usage, a delivery and a review.
func v1Fixture(t *testing.T, path string, mode dsnMode) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path, mode))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(migrationV1); err != nil {
		t.Fatal(err)
	}
	ctx := model.Context{ID: "c1", ProjectID: "p1", Version: 1, Digest: "sha256:ctx", Text: "private", Sources: []model.ContextSource{}, CreatedAt: "x"}
	reason, sha := delegateCandidate, "0123456789abcdef0123456789abcdef01234567"
	dlg := model.Task{ID: "td", ProjectID: "p1", Worktree: "/wd", State: model.TaskFirstDelivery, StateReason: &reason, Origin: delegateOrigin,
		CandidateSha: &sha, ContextRef: ctx.Ref(), Dependencies: []string{}}
	mgd := model.Task{ID: "tm", ProjectID: "p1", Worktree: "/wm", State: model.TaskFirstDelivery, ContextRef: ctx.Ref(), Dependencies: []string{}}
	in, total := int64(7), int64(9)
	usage := model.Usage{Tokens: model.TokenCounts{Input: &in, Total: &total}, UsageCompleteness: model.UsageComplete}
	run := model.Run{ID: "r1", TaskID: "td", Role: "developer", State: model.RunSucceeded}
	for _, q := range [][]any{
		{"INSERT INTO projects (id, payload) VALUES (?, ?)", "p1", mustJSON(model.Project{ID: "p1", Name: "P", Repositories: []string{"/r"}})},
		{"INSERT INTO contexts (id, version, project_id, digest, payload) VALUES (?, ?, ?, ?, ?)", "c1", 1, "p1", ctx.Digest, mustJSON(ctx)},
		{"INSERT INTO tasks (id, project_id, worktree, state, context_id, context_version, payload) VALUES (?, ?, ?, ?, ?, ?, ?)",
			dlg.ID, "p1", dlg.Worktree, dlg.State, "c1", 1, mustJSON(dlg)},
		{"INSERT INTO tasks (id, project_id, worktree, state, context_id, context_version, payload) VALUES (?, ?, ?, ?, ?, ?, ?)",
			mgd.ID, "p1", mgd.Worktree, mgd.State, "c1", 1, mustJSON(mgd)},
		{"INSERT INTO runs (id, task_id, state, lease_token, payload) VALUES (?, ?, ?, ?, ?)", "r1", "td", run.State, "", mustJSON(run)},
		{"INSERT INTO run_events (run_id, seq, payload) VALUES (?, ?, ?)", "r1", 0, mustJSON(model.RunEvent{Type: "tool", Summary: "bash"})},
		{"INSERT INTO usage (run_id, payload) VALUES (?, ?)", "r1", mustJSON(usage)},
		{"INSERT INTO deliveries (id, task_id, payload) VALUES (?, ?, ?)", "d1", "td",
			mustJSON(model.Delivery{ID: "d1", TaskID: "td", State: "first", CandidateSha: sha, ContextRef: ctx.Ref(), RunIDs: []string{"r1"}})},
		{"INSERT INTO reviews (id, task_id, run_id, payload) VALUES (?, ?, ?, ?)", "v1", "tm", "r1", mustJSON(model.Review{ID: "v1", TaskID: "tm", RunID: "r1", Verdict: "pass"})},
	} {
		if _, err := db.Exec(q[0].(string), q[1:]...); err != nil {
			t.Fatal(err)
		}
	}
}

// dumpRows returns every row of every schema table as text, for byte-exact before/after comparison.
func dumpRows(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path, dsnReadOnlyFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string][]string{}
	for _, tb := range schemaTables {
		rows, err := db.Query("SELECT * FROM " + tb + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out[tb] = append(out[tb], fmt.Sprintf("%q", vals))
		}
		rows.Close()
	}
	return out
}

func schemaInfo(t *testing.T, path string) (int, string) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path, dsnReadOnlyFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	var idx string
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'tasks_active_worktree'").Scan(&idx); err != nil {
		t.Fatal(err)
	}
	return v, idx
}

// assertV2History checks that the v1 fixture history survived migration and that the v2 index semantics hold.
func assertV2History(t *testing.T, s *Store, path string) {
	t.Helper()
	if v, idx := schemaInfo(t, path); v != schemaVersion || !strings.Contains(idx, "json_extract(payload, '$.stateReason')") {
		t.Fatalf("schema v%d index %s", v, idx)
	}
	st, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Tasks) != 2 || len(st.Runs) != 1 || len(st.Deliveries) != 1 || len(st.Reviews) != 1 || len(st.Contexts) != 1 {
		t.Fatalf("history lost: %+v", st)
	}
	td := st.Tasks[0]
	if td.State != model.TaskFirstDelivery || td.Origin != delegateOrigin || *td.StateReason != delegateCandidate || *td.CandidateSha != st.Deliveries[0].CandidateSha {
		t.Fatalf("delegate task changed: %+v", td)
	}
	if r := st.Runs[0]; r.Usage == nil || *r.Usage.Tokens.Input != 7 || *r.Usage.Tokens.Total != 9 || len(r.Events) != 1 || st.Deliveries[0].RunIDs[0] != "r1" {
		t.Fatalf("run history changed: %+v", r)
	}
	// The settled delegate worktree accepts a new active task; the managed first_delivery worktree stays exclusive.
	if err := s.Update(func(st *model.State) error {
		st.Tasks = append(st.Tasks, model.Task{ID: "tn", ProjectID: "p1", Worktree: "/wd", State: model.TaskImplementing, ContextRef: st.Contexts[0].Ref()})
		return nil
	}); err != nil {
		t.Fatalf("new active task on settled delegate worktree: %v", err)
	}
	if err := s.Update(func(st *model.State) error {
		st.Tasks = append(st.Tasks, model.Task{ID: "tx", ProjectID: "p1", Worktree: "/wm", State: model.TaskImplementing, ContextRef: st.Contexts[0].Ref()})
		return nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("managed first_delivery worktree not exclusive: %v", err)
	}
}

func TestMigrateV1ToV2PreservesHistory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, dbName)
	v1Fixture(t, path, dsnWriter)
	if v, idx := schemaInfo(t, path); v != schemaV1 || strings.Contains(idx, "json_extract") {
		t.Fatalf("fixture is not v1: %d %s", v, idx)
	}
	before := dumpRows(t, path)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if after := dumpRows(t, path); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("migration changed rows:\n%v\n%v", before, after)
	}
	assertV2History(t, s, path)
	// Reopening a v2 store is a no-op.
	_, idx := schemaInfo(t, path)
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if v, idx2 := schemaInfo(t, path); v != schemaVersion || idx2 != idx {
		t.Fatal("reopen changed schema")
	}
}

func TestOpenFreshIsV2AndRefusesFuture(t *testing.T) {
	_, dir := openTemp(t)
	path := filepath.Join(dir, dbName)
	if v, idx := schemaInfo(t, path); v != schemaVersion || !strings.Contains(idx, "json_extract(payload, '$.origin')") {
		t.Fatalf("fresh schema v%d %s", v, idx)
	}
	future := filepath.Join(t.TempDir(), "future")
	os.Mkdir(future, 0o700)
	db, err := sql.Open("sqlite", dsn(filepath.Join(future, dbName), dsnWriter))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE x (a); PRAGMA user_version = 7"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(future); err == nil || !strings.Contains(err.Error(), "unsupported schema version 7") {
		if s != nil {
			s.Close()
		}
		t.Fatalf("future schema accepted: %v", err)
	}
	if v, _ := func() (int, error) {
		db, _ := sql.Open("sqlite", dsn(filepath.Join(future, dbName), dsnReadOnlyFile))
		defer db.Close()
		var v int
		return v, db.QueryRow("PRAGMA user_version").Scan(&v)
	}(); v != 7 {
		t.Fatal("future store modified")
	}
}

func TestActiveWorktreeDelegateExemption(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Update(func(st *model.State) error { seed(st); return nil }); err != nil {
		t.Fatal(err)
	}
	str := func(v string) *string { return &v }
	mk := func(id, wt, state, origin string, reason *string) model.Task {
		return model.Task{ID: id, ProjectID: "p1", Worktree: wt, State: state, Origin: origin, StateReason: reason,
			ContextRef: model.ContextRef{ID: "c1", Version: 1, Digest: "d1"}}
	}
	add := func(ts ...model.Task) error {
		return s.Update(func(st *model.State) error { st.Tasks = append(st.Tasks, ts...); return nil })
	}
	// Two settled delegate candidates and one new active task share a worktree.
	if err := add(mk("a1", "/wa", model.TaskFirstDelivery, delegateOrigin, str(delegateCandidate)),
		mk("a2", "/wa", model.TaskFirstDelivery, delegateOrigin, str(delegateCandidate)),
		mk("a3", "/wa", model.TaskImplementing, "", nil)); err != nil {
		t.Fatalf("settled delegates + active task refused: %v", err)
	}
	// Every non-exempt combination is still exclusive against an active task.
	for i, c := range []model.Task{
		mk("b", "", model.TaskFirstDelivery, "", nil),                                    // regular managed first_delivery
		mk("b", "", model.TaskFirstDelivery, delegateOrigin, nil),                        // missing reason
		mk("b", "", model.TaskFirstDelivery, delegateOrigin, str("other")),               // other reason
		mk("b", "", model.TaskFirstDelivery, model.OriginNative, str(delegateCandidate)), // other origin
		mk("b", "", model.TaskFirstDelivery, "", str(delegateCandidate)),                 // missing origin
		mk("b", "", model.TaskChecking, delegateOrigin, str(delegateCandidate)),          // incomplete delegate, other active state
		mk("b", "", model.TaskImplementing, delegateOrigin, nil),                         // running delegate
	} {
		wt := fmt.Sprintf("/wb%d", i)
		c.ID, c.Worktree = fmt.Sprintf("b%d", i), wt
		if err := add(c, mk(fmt.Sprintf("c%d", i), wt, model.TaskImplementing, "", nil)); !errors.Is(err, ErrConflict) {
			t.Fatalf("case %d bypassed exclusivity: %v", i, err)
		}
		if err := add(c, mk(fmt.Sprintf("c%d", i), wt, model.TaskFirstDelivery, "", nil)); !errors.Is(err, ErrConflict) {
			t.Fatalf("case %d bypassed exclusivity against managed first_delivery: %v", i, err)
		}
	}
	// Malformed or non-object payloads written directly cannot bypass the index either.
	for i, p := range []string{`{"origin":"native_delegate","stateReason":"delegate_candidate"`, `["native_delegate","delegate_candidate"]`,
		`{"origin":1,"stateReason":"delegate_candidate"}`, `{"origin":"native_delegate","stateReason":null}`, `not json`} {
		err := s.tx(func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO tasks (id, project_id, worktree, state, context_id, context_version, payload) VALUES (?, 'p1', '/w1', ?, 'c1', 1, ?)",
				fmt.Sprintf("m%d", i), model.TaskFirstDelivery, p)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Fatalf("payload %d bypassed exclusivity: %v", i, err)
		}
	}
}
