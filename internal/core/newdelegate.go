package core

import (
	"context"

	"github.com/hunknownz/Meerkat/internal/model"
)

// OriginDelegate marks a task prepared by Delegate: exactly one bounded developer run, never the review
// flow. It is persisted in the existing task Origin field so the mode survives restarts.
const OriginDelegate = "native_delegate"

// DelegateCandidate is the state reason of a delegated task whose developer commit was verified. The task
// stays first_delivery with a "first" candidate delivery: a local candidate, not delivered or AI-reviewed.
const DelegateCandidate = "delegate_candidate"

// isDelegateCandidate reports a settled delegate task; it is excluded from active-state handling.
func isDelegateCandidate(t model.Task) bool {
	return t.Origin == OriginDelegate && t.State == model.TaskFirstDelivery && deref(t.StateReason) == DelegateCandidate
}

// DryProfile is the public part of a frozen profile.
type DryProfile struct {
	Executor     string `json:"executor"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	ConfigDigest string `json:"configDigest"`
}

// DryRun lists safe public facts of a validated input. Nothing is recorded.
type DryRun struct {
	Valid         bool                  `json:"valid"`
	ProjectID     string                `json:"projectId"`
	Repository    string                `json:"repository"`
	Worktree      string                `json:"worktree"`
	Branch        string                `json:"branch"`
	Head          string                `json:"head"`
	Scope         []string              `json:"scope"`
	ContextDigest string                `json:"contextDigest"`
	Budget        model.Budget          `json:"budget"`
	Profiles      map[string]DryProfile `json:"profiles"`
	Mode          string                `json:"mode"`
}

// DryPrepare validates input, profiles and the clean linked worktree like Prepare but is pure: it reads
// state only to check dependencies/context versions and never writes, reads keys or spawns processes.
func (c *Core) DryPrepare(raw []byte) (DryRun, error) {
	var out DryRun
	v, err := c.validateInputFor(raw, true)
	if err != nil {
		return out, err
	}
	st, err := c.st.Read() // private copy; checkRefs may append to it but it is never written
	if err != nil {
		return out, err
	}
	if _, err := checkRefs(st, v); err != nil {
		return out, err
	}
	out = DryRun{Valid: true, ProjectID: v.projectID, Repository: v.facts.repository, Worktree: v.facts.worktree, Branch: v.facts.branch,
		Head: v.facts.head, Scope: v.scope, ContextDigest: v.ctx.Digest, Budget: *v.budget, Profiles: map[string]DryProfile{}, Mode: "delegate"}
	for role, p := range v.frozen {
		out.Profiles[role] = DryProfile{Executor: safeName(p.Executor), Provider: safeName(p.Provider), Model: safeName(p.Model), ConfigDigest: p.ConfigDigest}
	}
	return out, nil
}

// Delegate prepares one task and runs ONLY its developer through the service-owned scheduler with the
// usual process, lease, budget, scope and Git checks. Success leaves a local candidate for external review.
func (c *Core) Delegate(ctx context.Context, raw []byte) (Result, error) {
	return c.dispatchLocked(ctx, func() ([]string, error) {
		t, err := c.prepare(raw, OriginDelegate)
		return []string{t.ID}, err
	}, false, false, true)
}

// ResumeDelegate continues the original developer-only task. Selection verifies
// its origin, checkpoint, session and remaining budget without preparing a new task.
func (c *Core) ResumeDelegate(ctx context.Context, taskID string) (Result, error) {
	return c.dispatchLocked(ctx, func() ([]string, error) {
		return []string{taskID}, nil
	}, true, false, true)
}
