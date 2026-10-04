package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func TestResumeDelegateKeepsDeveloperOnlyCheckpointAndUsage(t *testing.T) {
	e, f := checkpointEnv(t)
	task := e.prepare(e.worktree("delegate-resume"), nil)
	if err := e.c.update(func(st *model.State) error {
		findTask(st, task.ID).Origin = OriginDelegate
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := e.c.dispatchLocked(context.Background(), func() ([]string, error) { return []string{task.ID}, nil }, false, false, true)
	if err != nil || first.Tasks[0].State != model.TaskPaused {
		t.Fatal(first, err)
	}
	before := e.state()
	checkpoints, _ := e.st.CheckpointsForTask(task.ID)
	result, err := e.c.ResumeDelegate(context.Background(), task.ID)
	if err != nil || result.Mode != "delegate" || result.Tasks[0].State != model.TaskFirstDelivery || deref(result.Tasks[0].StateReason) != DelegateCandidate {
		t.Fatal(result, err)
	}
	after := e.state()
	if len(before.Tasks) != len(after.Tasks) || len(before.Contexts) != len(after.Contexts) || len(after.Runs) != 2 || f.calls != 2 {
		t.Fatal("continuation created a new task or ran extra roles")
	}
	for _, r := range after.Runs {
		if r.Role != "developer" {
			t.Fatal("delegate entered review workflow")
		}
	}
	consumed, _ := e.st.CheckpointsForTask(task.ID)
	if consumed[0].State != "consumed" || consumed[0].SessionID != checkpoints[0].SessionID {
		t.Fatal("original checkpoint/session was replaced")
	}
	used, _ := budgetUse(after, task.ID)
	if used != 200 {
		t.Fatal("previous usage lost or duplicated", used)
	}
	if _, err := e.c.ResumeDelegate(context.Background(), task.ID); err == nil || f.calls != 2 {
		t.Fatal("candidate replay accepted", err)
	}
}

func TestResumeDelegateRejectsWorkflowAndChangedCheckpoint(t *testing.T) {
	for _, kind := range []string{"workflow", "changed-file"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			task := e.prepare(e.worktree("delegate-rejected"), nil)
			delegate := kind != "workflow"
			if delegate {
				if err := e.c.update(func(st *model.State) error { findTask(st, task.ID).Origin = OriginDelegate; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.c.dispatchLocked(context.Background(), func() ([]string, error) { return []string{task.ID}, nil }, false, false, delegate); err != nil {
				t.Fatal(err)
			}
			if kind == "changed-file" {
				if err := os.WriteFile(filepath.Join(task.Worktree, "src", "partial.txt"), []byte("unexpected edit"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.c.ResumeDelegate(context.Background(), task.ID); err == nil || f.calls != 1 {
				t.Fatal("unverified continuation accepted", err)
			}
		})
	}
}
