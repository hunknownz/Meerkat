package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
