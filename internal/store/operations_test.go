package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

const operationTask = "10000000-0000-4000-8000-000000000001"
const operationID = "10000000-0000-4000-8000-000000000002"
const operationRequest = "10000000-0000-4000-8000-000000000003"

func queueFixture(t *testing.T) (*Store, model.ControllerLease, model.Operation) {
	t.Helper()
	s, _ := openTemp(t)
	err := s.Update(func(st *model.State) error {
		st.Projects = []model.Project{{ID: "p"}}
		st.Contexts = []model.Context{{ID: "c", ProjectID: "p", Version: 1, Digest: "d", Text: "private"}}
		st.Tasks = []model.Task{{ID: operationTask, ProjectID: "p", Worktree: "/neutral/worktree", State: model.TaskReady, ContextRef: st.Contexts[0].Ref()}}
		st.Runs = []model.Run{{ID: "old", TaskID: operationTask, State: model.RunSucceeded, Usage: &model.Usage{UsageCompleteness: model.UsageUnknown}, UpdatedAt: now()}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.AcquireLease()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ReleaseLease(l.Token) })
	o := model.Operation{SchemaVersion: 1, ID: operationID, RequestID: operationRequest, RequestDigest: strings.Repeat("a", 64), Mode: "workflow",
		State: model.OperationQueued, CreatedAt: now(), UpdatedAt: now(), FrozenFixRounds: 2,
		Tasks: []model.OperationTask{{TaskID: operationTask, State: model.MemberQueued, ContractDigest: strings.Repeat("b", 64), TaskState: model.TaskQueued}}}
	return s, l, o
}

func TestOperationsAtomicOwnershipDedupBackupAndFencing(t *testing.T) {
	s, l, o := queueFixture(t)
	put := func(op model.Operation) (model.Operation, bool, error) {
		return s.EnqueueOwned(l.Token, op, func(st *model.State) error { st.Tasks[0].State = model.TaskQueued; return nil })
	}
	if _, duplicate, err := put(o); err != nil || duplicate {
		t.Fatal(duplicate, err)
	}
	if _, duplicate, err := put(o); err != nil || !duplicate {
		t.Fatal(duplicate, err)
	}
	conflict := o
	conflict.RequestDigest = strings.Repeat("c", 64)
	if _, _, err := put(conflict); !errors.Is(err, ErrConflict) {
		t.Fatal("request mismatch accepted", err)
	}
	conflict = o
	conflict.ID, conflict.RequestID = "10000000-0000-4000-8000-000000000004", "10000000-0000-4000-8000-000000000005"
	if _, _, err := put(conflict); !errors.Is(err, ErrConflict) {
		t.Fatal("task double ownership accepted", err)
	}
	// Ordinary state writes recreate rows, but must retain operation membership.
	if err := s.UpdateOwned(l.Token, func(st *model.State) error { st.Tasks[0].Title = "updated"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Operation(o.ID); err != nil {
		t.Fatal("membership lost in state write", err)
	}
	if err := s.UpdateOperationOwned("foreign", o.ID, func(*model.Operation, *model.State) error { t.Fatal("foreign callback ran"); return nil }); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	if err := s.UpdateOperationOwned(l.Token, o.ID, func(op *model.Operation, st *model.State) error {
		op.RequestDigest = strings.Repeat("d", 64)
		st.Tasks[0].Title = "must roll back"
		return nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	st, _ := s.Read()
	if st.Tasks[0].Title != "updated" || st.Runs[0].Usage.Tokens.Total != nil {
		t.Fatal("rollback or unknown usage lost")
	}
	backup := filepath.Join(t.TempDir(), "queue.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "restored")
	if err := Restore(backup, dir); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if old, err := restored.OperationByRequest(o.RequestID); err != nil || old.ID != o.ID || old.RequestDigest != o.RequestDigest || old.Tasks[0].ContractDigest != o.Tasks[0].ContractDigest {
		t.Fatal(old, err)
	}
	st, _ = restored.Read()
	if len(st.Runs) != 1 || st.Runs[0].Usage.Tokens.Total != nil {
		t.Fatal("backup changed unknown usage")
	}
}

func TestOperationBackupRejectsPayloadIndexConflict(t *testing.T) {
	s, l, o := queueFixture(t)
	if _, _, err := s.EnqueueOwned(l.Token, o, func(*model.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "queue.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn(backup, dsnBackupFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE operations SET request_digest = ?", strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := ValidateBackup(backup); !errors.Is(err, ErrBadBackup) {
		t.Fatal("corrupt queue backup accepted", err)
	}
}

func TestOperationRejectsMalformedResultBeforeWriting(t *testing.T) {
	s, l, o := queueFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*model.Operation)
	}{
		{"timestamp", func(op *model.Operation) { at := "not-a-time"; op.Tasks[0].StartedAt = &at }},
		{"candidate", func(op *model.Operation) { sha := "not-a-sha"; op.Tasks[0].CandidateSHA = &sha }},
		{"completed-without-outcome", func(op *model.Operation) { op.Tasks[0].State = model.MemberCompleted }},
		{"running-without-start", func(op *model.Operation) { op.Tasks[0].State = model.MemberRunning }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := o
			bad.Tasks = append([]model.OperationTask{}, o.Tasks...)
			tc.change(&bad)
			if _, _, err := s.EnqueueOwned(l.Token, bad, func(st *model.State) error { st.Tasks[0].State = model.TaskQueued; return nil }); err == nil {
				t.Fatal("malformed result accepted")
			}
			st, err := s.Read()
			if err != nil || st.Tasks[0].State != model.TaskReady {
				t.Fatal("invalid queue write was not atomic", err)
			}
		})
	}
}
