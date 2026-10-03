package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func sessionFixture(t *testing.T) (*Store, model.ControllerLease, model.Session, string) {
	t.Helper()
	s, l, o := queueFixture(t)
	id := "40000000-0000-4000-8000-000000000001"
	runID := "40000000-0000-4000-8000-000000000002"
	root := filepath.Join(s.DataDir(), "sessions")
	os.Mkdir(root, 0o700)
	os.Mkdir(filepath.Join(root, id), 0o700)
	b := []byte("private opaque history\n")
	ss := model.Session{ID: id, TaskID: o.Tasks[0].TaskID, Role: "developer", Executor: "fixture", ProfileID: "profile", ProfileDigest: strings.Repeat("a", 64), ContractDigest: strings.Repeat("b", 64), ProviderID: "opaque-id", FileRef: SessionFileRef(id), FileDigest: fileDigest(b), LastSHA: strings.Repeat("c", 40), State: model.SessionIdle, CreatedAt: now(), UpdatedAt: now()}
	os.WriteFile(filepath.Join(s.dir, filepath.FromSlash(ss.FileRef)), b, 0o600)
	return s, l, ss, runID
}

func startSession(t *testing.T, s *Store, l model.ControllerLease, ss model.Session, runID string) {
	t.Helper()
	if err := s.StartSessionRunOwned(l.Token, ss, runID, true, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: runID, TaskID: ss.TaskID, State: model.RunStarting, UpdatedAt: now()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAtomicOwnershipBackupRestoreAndReconcile(t *testing.T) {
	s, l, ss, rid := sessionFixture(t)
	startSession(t, s, l, ss, rid)
	other := ss
	other.ID = "40000000-0000-4000-8000-000000000003"
	other.FileRef = SessionFileRef(other.ID)
	if err := s.StartSessionRunOwned(l.Token, other, "40000000-0000-4000-8000-000000000004", true, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: "40000000-0000-4000-8000-000000000004", TaskID: ss.TaskID, State: model.RunStarting})
		return nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatal("double session owner", err)
	}
	activeBackup := filepath.Join(t.TempDir(), "active.db")
	if err := s.Backup(activeBackup); !errors.Is(err, ErrConflict) {
		t.Fatal("active history advertised as consistent", err)
	}
	if _, err := os.Stat(activeBackup); !os.IsNotExist(err) {
		t.Fatal("partial backup retained")
	}
	if err := s.FinishSessionRunOwned("foreign", ss, rid, func(*model.State) error { t.Fatal("foreign callback"); return nil }); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	ss.UpdatedAt = now()
	if err := s.FinishSessionRunOwned(l.Token, ss, rid, func(st *model.State) error { st.Runs[len(st.Runs)-1].State = model.RunSucceeded; return nil }); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "idle.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := Restore(backup, dst); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.SessionForRun(rid)
	if err != nil || got.FileDigest != ss.FileDigest || got.State != model.SessionIdle {
		t.Fatal(got, err)
	}
	b, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(got.FileRef)))
	if err != nil || fileDigest(b) != ss.FileDigest {
		t.Fatal("history restore lost", err)
	}
	rid2 := "40000000-0000-4000-8000-000000000005"
	if err := s.StartSessionRunOwned(l.Token, ss, rid2, false, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: rid2, TaskID: ss.TaskID, State: model.RunStarting})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileSessionsOwned(l.Token); err != nil {
		t.Fatal(err)
	}
	got, err = s.SessionForRun(rid2)
	if err != nil || got.State != model.SessionUnknown {
		t.Fatal("unconfirmed restart not unknown", got, err)
	}
	if err := s.StartSessionRunOwned(l.Token, got, "40000000-0000-4000-8000-000000000006", false, func(*model.State) error { t.Fatal("unknown history callback"); return nil }); err == nil {
		t.Fatal("unknown session replayed")
	}
}

func TestSessionBackupRejectsCorruptionAndPreservesUnknownBytes(t *testing.T) {
	s, l, ss, rid := sessionFixture(t)
	startSession(t, s, l, ss, rid)
	ss.State, ss.UpdatedAt = model.SessionUnknown, now()
	if err := s.FinishSessionRunOwned(l.Token, ss, rid, func(st *model.State) error { st.Runs[len(st.Runs)-1].State = model.RunUnknown; return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, filepath.FromSlash(ss.FileRef))
	os.WriteFile(path, []byte("new unverified history\n"), 0o600)
	backup := filepath.Join(t.TempDir(), "unknown.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := Restore(backup, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, filepath.FromSlash(ss.FileRef)))
	if string(b) != "new unverified history\n" {
		t.Fatal("unknown evidence discarded")
	}
	db, err := sql.Open("sqlite", dsn(backup, dsnBackupFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE "+sessionBundleTable+" SET body=?", []byte("tampered")); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := ValidateBackup(backup); !errors.Is(err, ErrBadBackup) {
		t.Fatal("corrupt history accepted", err)
	}
}
