package core

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

// An explicitly declared dependency in the same worktree may advance HEAD to
// exactly its delivered candidate.
func TestFrozenHeadExplicitDependencyProgression(t *testing.T) {
	e := setup(t)
	e.fx.maxActive = atomic.Int32{}
	wt := e.worktree("dep")
	c1 := e.prepare(wt, nil)
	c2 := e.prepare(wt, func(m map[string]any) { m["dependencies"] = []any{c1.ID} })
	res := e.exec(c1.ID, c2.ID)
	want(t, res, 0, model.TaskDelivered, "")
	want(t, res, 1, model.TaskDelivered, "")
	if e.fx.maxActive.Load() != 1 {
		t.Fatal("same worktree ran concurrently")
	}
}

// HEAD moved to an unrelated task's delivered candidate in the same repository
// (descendant of the baseline) is refused.
func TestFrozenHeadRefusesUnrelatedDeliveredCandidate(t *testing.T) {
	e := setup(t)
	a := e.prepare(e.worktree("ua"), nil)
	wb := e.worktree("ub")
	b := e.prepare(wb, nil)
	want(t, e.exec(a.ID), 0, model.TaskDelivered, "")
	cand := deref(taskOf(e.state(), a.ID).CandidateSha)
	if cand == "" || !isAncestor(wb, deref(b.BaselineSha), cand) {
		t.Fatal("setup: candidate must descend from b's baseline")
	}
	sh(t, wb, "reset", "-q", "--hard", cand)
	want(t, e.exec(b.ID), 0, model.TaskFailed, "baseline_changed_requires_prepare")
}

// HEAD diverged from the frozen baseline is refused.
func TestFrozenHeadRefusesDivergence(t *testing.T) {
	e := setup(t)
	wt := e.worktree("div")
	task := e.prepare(wt, nil)
	tree, err := git(wt, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := git(wt, "commit-tree", strings.TrimSpace(tree), "-m", "orphan")
	if err != nil {
		t.Fatal(err)
	}
	sh(t, wt, "reset", "-q", "--hard", strings.TrimSpace(orphan))
	want(t, e.exec(task.ID), 0, model.TaskFailed, "baseline_changed_requires_prepare")
}
