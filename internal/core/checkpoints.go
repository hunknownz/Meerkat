package core

import (
	"slices"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

func (c *Core) savedCheckpoint(taskID string) (*model.Checkpoint, error) {
	all, e := c.st.CheckpointsForTask(taskID)
	if e != nil {
		return nil, e
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].State == "saved" {
			return &all[i], nil
		}
	}
	return nil, nil
}
func (c *Core) verifyCheckpoint(st *model.State, t model.Task, cp model.Checkpoint, s step) error {
	prof, why := c.verifiedProfile(st, model.Settings{}, t, s.role)
	if why != "" || cp.TaskID != t.ID || cp.Role != s.role || cp.Purpose != s.purpose || cp.Worktree != t.Worktree || cp.Branch != deref(t.Branch) || cp.ContractDigest != taskContract(st, t) || cp.ProfileDigest != profileContract(prof) || cp.State != "saved" {
		return invalid("checkpoint frozen contract changed")
	}
	if _, why := frozenOK(st, t); why != "" {
		return invalid("checkpoint context changed")
	}
	ss, e := c.st.SessionForRun(cp.RunID)
	if e != nil || ss.ID != cp.SessionID || ss.State != model.SessionIdle || ss.ActiveRunID != nil || ss.FileDigest != cp.SessionDigest || ss.LastSHA != cp.HeadSHA {
		return invalid("checkpoint session is unresolved")
	}
	x, ok := c.reg[execName(prof)].(executor.StatefulExecutor)
	if !ok {
		return invalid("checkpoint executor cannot inspect sessions")
	}
	history, e := x.InspectSession(c.sessionBinding(ss, t.Worktree))
	if e != nil || history.ProviderID != ss.ProviderID || history.Digest != ss.FileDigest {
		return invalid("checkpoint session history changed")
	}
	if _, e := c.st.ReadCheckpointFile(cp); e != nil {
		return invalid("checkpoint private evidence changed")
	}
	if checkpoint.Verify(t.Worktree, checkpoint.Binding{Digest: cp.FileDigest, Scope: t.Scope}) != nil {
		return invalid("checkpoint files, index, HEAD or worktree changed")
	}
	return nil
}

func (c *Core) captureCheckpoint(st *model.State, t model.Task, ss model.Session, runID, base string, s step) (*model.Checkpoint, error) {
	snap, raw, e := checkpoint.Capture(t.Worktree, t.Scope)
	if e != nil || snap.Head != base || snap.Branch != deref(t.Branch) {
		return nil, invalid("checkpoint worktree is unverifiable")
	}
	if checkpoint.Verify(t.Worktree, checkpoint.Binding{Digest: checkpoint.Digest(raw), Scope: t.Scope}) != nil {
		return nil, invalid("worktree changed during checkpoint")
	}
	fresh, e := c.st.Read()
	if e != nil {
		return nil, e
	}
	current := findTask(fresh, t.ID)
	if current == nil || taskContract(fresh, *current) != ss.ContractDigest {
		return nil, invalid("checkpoint task changed")
	}
	run := findRun(fresh, runID)
	seq := 0
	if run != nil {
		seq = len(run.Events)
	}
	cp := model.Checkpoint{ID: newUUID(), TaskID: t.ID, RunID: runID, SessionID: ss.ID, SessionDigest: ss.FileDigest, ContractDigest: ss.ContractDigest, ProfileDigest: ss.ProfileDigest,
		Role: s.role, Purpose: s.purpose, Worktree: t.Worktree, BaselineSHA: base, HeadSHA: snap.Head, Branch: snap.Branch, FileDigest: checkpoint.Digest(raw), FileCount: len(snap.Files), LastEventSeq: seq, State: "saved", CreatedAt: now()}
	cp.FileRef = store.CheckpointFileRef(cp.ID)
	if e := c.st.WriteCheckpointFile(cp, raw); e != nil {
		return nil, e
	}
	return &cp, nil
}

func checkpointSummary(c model.Checkpoint) model.CheckpointSummary {
	return model.CheckpointSummary{ID: c.ID, RunID: c.RunID, Role: c.Role, HeadSHA: c.HeadSHA, FileCount: c.FileCount, State: c.State, ResumedRunID: c.ResumedRunID, CreatedAt: c.CreatedAt}
}
func (c *Core) checkpointOccupied(task model.Task) bool {
	all, e := c.st.CheckpointsForTask("")
	if e != nil {
		return true
	}
	return slices.ContainsFunc(all, func(cp model.Checkpoint) bool {
		return cp.State == "saved" && cp.TaskID != task.ID && cp.Worktree == task.Worktree
	})
}
