package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/platform"
)

// TaskResult is the per-task outcome of one dispatch.
type TaskResult struct {
	ID           string  `json:"id"`
	State        string  `json:"state"`
	StateReason  *string `json:"stateReason"`
	CandidateSha *string `json:"candidateSha"`
	ResumeRole   *string `json:"resumeRole"`
}

// Result is the outcome of Execute.
type Result struct {
	Mode    string       `json:"mode,omitempty"` // "delegate": developer-only local candidate, not reviewed
	Fatal   string       `json:"fatal,omitempty"`
	Stopped string       `json:"stopped,omitempty"`
	Tasks   []TaskResult `json:"tasks"`
}

type step struct {
	role, purpose, taskState, done, reason string
}

type pipeline struct {
	implemented, polished bool
	verdict               string
	fixRounds             int
	lastReviewRunID       string
	startSha              string
}

type limits struct {
	MaxTokens      int64 `json:"maxTokens"`
	MaxWallSeconds int64 `json:"maxWallSeconds"`
}

type reportView struct {
	Summary   string             `json:"summary"`
	Checks    []executor.Check   `json:"checks"`
	KnownGaps []string           `json:"knownGaps"`
	Findings  []executor.Finding `json:"findings,omitempty"`
}

// runSummary is the core-written, secret-free run summary.
type runSummary struct {
	Purpose        string      `json:"purpose"`
	FixRound       int         `json:"fixRound,omitempty"`
	Limits         limits      `json:"limits"`
	Outcome        string      `json:"outcome,omitempty"`
	ErrorCategory  string      `json:"errorCategory,omitempty"`
	BaselineSha    string      `json:"baselineSha,omitempty"`
	ResultSha      string      `json:"resultSha,omitempty"`
	StartSha       string      `json:"startSha,omitempty"`
	ChangedPaths   []string    `json:"changedPaths,omitempty"`
	Verdict        string      `json:"verdict,omitempty"`
	Decision       string      `json:"decision,omitempty"`
	Report         *reportView `json:"report,omitempty"`
	CheckpointID   string      `json:"checkpointId,omitempty"`
	BudgetRevision int64       `json:"budgetRevision,omitempty"`
}

func summaryOf(r model.Run) runSummary {
	var s runSummary
	_ = json.Unmarshal(r.Summary, &s)
	return s
}

func pipelineOf(s *model.State, taskID string) pipeline {
	var p pipeline
	for _, r := range s.Runs {
		if r.TaskID != taskID || r.State != model.RunSucceeded {
			continue
		}
		sm := summaryOf(r)
		switch r.Role {
		case "developer":
			if !p.implemented {
				p.startSha = sm.StartSha
			}
			p.implemented, p.verdict = true, ""
			if sm.Purpose == "fix" {
				p.fixRounds++
			}
		case "reviewer":
			p.verdict, p.lastReviewRunID = sm.Verdict, r.ID
		case "polisher":
			p.polished, p.verdict = true, ""
		}
	}
	return p
}

func nextStep(p pipeline, maxFix int) step {
	switch {
	case !p.implemented:
		return step{role: "developer", purpose: "implement", taskState: model.TaskImplementing}
	case p.verdict == "" && p.polished:
		return step{role: "reviewer", purpose: "recheck", taskState: model.TaskRechecking}
	case p.verdict == "":
		return step{role: "reviewer", purpose: "check", taskState: model.TaskChecking}
	case p.verdict == "changes_requested":
		if p.fixRounds >= maxFix {
			return step{done: model.TaskBlocked, reason: "fix_rounds_exhausted"}
		}
		return step{role: "developer", purpose: "fix", taskState: model.TaskFixing}
	case p.verdict == "pass" && p.polished:
		return step{done: model.TaskDelivered}
	case p.verdict == "pass":
		return step{role: "polisher", purpose: "polish", taskState: model.TaskPolishing}
	}
	return step{done: model.TaskFailed, reason: "unknown_verdict"}
}

func deliveredCandidate(s *model.State, taskID string) *model.Delivery {
	for i := len(s.Deliveries) - 1; i >= 0; i-- {
		d := &s.Deliveries[i]
		if d.TaskID == taskID && d.State == "delivered" && shaRE.MatchString(d.CandidateSha) {
			return d
		}
	}
	return nil
}

// expectedHead is the commit a role must start from (uses Git; call outside transactions).
func expectedHead(s *model.State, t model.Task, p pipeline, head string) string {
	if p.implemented {
		return deref(t.CandidateSha)
	}
	base := deref(t.BaselineSha)
	if head == "" {
		return ""
	}
	if head == base {
		return head
	}
	if !isAncestor(t.Worktree, base, head) {
		return ""
	}
	// The baseline is frozen at prepare. HEAD may only have moved to the exact
	// delivered candidate of an explicitly declared dependency in the same
	// project and repository; unrelated tasks' candidates are never adopted.
	for _, id := range t.Dependencies {
		o := findTask(s, id)
		if o == nil || o.ID == t.ID || o.ProjectID != t.ProjectID || o.Repository != t.Repository || o.State != model.TaskDelivered {
			continue
		}
		d := deliveredCandidate(s, o.ID)
		if d != nil && d.CandidateSha == head && deref(o.CandidateSha) == head && d.Repository == t.Repository {
			return head
		}
	}
	return ""
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func sp(s string) *string { return &s }

// verifiedProfile resolves the effective profile and re-reads its config; a changed digest is refused.
func (c *Core) verifiedProfile(s *model.State, set model.Settings, t model.Task, role string) (model.Profile, string) {
	id := t.ProfileIDs[role]
	i := slices.IndexFunc(s.Profiles, func(p model.Profile) bool { return p.ID == id })
	if i < 0 || s.Profiles[i].ProjectID != t.ProjectID || s.Profiles[i].Role != role {
		return model.Profile{}, "profile_missing"
	}
	p := s.Profiles[i]
	if _, ok := c.reg[execName(p)]; !ok {
		return p, "executor_unregistered"
	}
	fresh, err := freezeProfile(p.ConfigFile, role, t.ProjectID)
	if err != nil || fresh.ConfigDigest != p.ConfigDigest || profileDigest(p) != p.ConfigDigest {
		return p, "profile_changed_requires_prepare"
	}
	if p.Limits.MaxTokens < 1 || p.Limits.MaxTokens > maxProfileTokens || p.Limits.MaxWallSeconds < 1 || p.Limits.MaxWallSeconds > maxProfileWall {
		return p, "profile_limits_invalid"
	}
	return p, ""
}

// budgetUse sums task usage conservatively: unknown token or wall usage counts as the run's cap.
func budgetUse(s *model.State, taskID string) (int64, float64) {
	var tokens int64
	var secs float64
	for _, r := range s.Runs {
		if r.TaskID != taskID {
			continue
		}
		sm := summaryOf(r)
		if r.Usage != nil && r.Usage.Tokens.Total != nil {
			tokens += *r.Usage.Tokens.Total
		} else if model.RunHadProcess(r) || !model.IsTerminalRunState(r.State) {
			tokens += sm.Limits.MaxTokens
		}
		a, e1 := time.Parse(time.RFC3339Nano, r.StartedAt)
		if r.EndedAt != nil {
			if b, e2 := time.Parse(time.RFC3339Nano, *r.EndedAt); e1 == nil && e2 == nil && b.After(a) {
				secs += b.Sub(a).Seconds()
			}
		} else if !model.IsTerminalRunState(r.State) {
			secs += float64(sm.Limits.MaxWallSeconds)
		}
	}
	return tokens, secs
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return platform.Alive(pid)
}

// Execute dispatches the selected tasks through developer -> reviewer -> (bounded fix -> reviewer) ->
// polisher -> fresh reviewer -> delivered. Overlapping dispatches are rejected with ErrBusy.
func (c *Core) Execute(ctx context.Context, taskIDs []string, resume, acknowledge bool) (Result, error) {
	var res Result
	if len(taskIDs) < 1 || len(taskIDs) > 50 {
		return res, invalid("taskIds must list 1..50 task UUIDs")
	}
	ids := []string{}
	for _, id := range taskIDs {
		if !uuidRE.MatchString(id) {
			return res, invalid("taskIds must be lowercase task UUIDs")
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return c.dispatchLocked(ctx, func() ([]string, error) { return ids, nil }, resume, acknowledge, false)
}

// dispatchLocked takes the single dispatch slot, resolves the task ids (Delegate prepares here so a busy
// core never leaves an orphaned task) and schedules them.
func (c *Core) dispatchLocked(ctx context.Context, sel func() ([]string, error), resume, acknowledge, delegate bool) (Result, error) {
	var res Result
	if !c.dispatch.TryLock() {
		return res, ErrBusy
	}
	defer c.dispatch.Unlock()
	c.mu.Lock()
	if closed, lost := c.closed, c.lost; closed || lost {
		c.mu.Unlock()
		if closed {
			return res, ErrClosed
		}
		return res, ErrLeaseLost
	}
	c.mu.Unlock()
	ids, err := sel()
	if err != nil {
		return res, err
	}
	mode := "workflow"
	if delegate {
		mode = "delegate"
	}
	rc, err := c.submit(model.DispatchRequest{RequestID: newUUID(), TaskIDs: ids, Resume: resume, Acknowledge: acknowledge}, mode)
	if err != nil {
		return res, err
	}
	res, err = c.waitResult(ctx, rc.Operation.ID)
	if err != nil {
		return res, err
	}
	ordered := []TaskResult{}
	for _, id := range ids {
		for _, task := range res.Tasks {
			if task.ID == id {
				ordered = append(ordered, task)
				break
			}
		}
	}
	res.Tasks = ordered
	return res, nil
}

func (c *Core) isClosed() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed }

// validateSelection checks the selection on a snapshot (Git/process facts gathered here, outside SQL).
func (c *Core) validateSelection(st *model.State, set model.Settings, ids []string, resume, acknowledge, delegate bool) ([]string, map[string]string, error) {
	states := map[string]string{}
	var ack []string
	done, visiting := map[string]bool{}, map[string]bool{}
	var visit func(t *model.Task) error
	visit = func(t *model.Task) error {
		if done[t.ID] {
			return nil
		}
		if visiting[t.ID] {
			return invalid("task dependencies contain a cycle")
		}
		visiting[t.ID] = true
		for _, d := range t.Dependencies {
			dt := findTask(st, d)
			if dt == nil || dt.ProjectID != t.ProjectID {
				return invalid("task dependency is missing or belongs to a different project")
			}
			if err := visit(dt); err != nil {
				return err
			}
		}
		visiting[t.ID], done[t.ID] = false, true
		return nil
	}
	for _, id := range ids {
		t := findTask(st, id)
		if t == nil {
			return nil, nil, invalid("task does not exist")
		}
		if err := visit(t); err != nil {
			return nil, nil, err
		}
		if (t.Origin == OriginDelegate) != delegate {
			return nil, nil, invalid("delegated tasks run only their developer via run; workflow execute refuses them")
		}
		resumable := slices.Contains([]string{model.TaskFailed, model.TaskStopped, model.TaskPaused, model.TaskUnknown}, t.State)
		readyLike := t.State == model.TaskReady || (t.State == model.TaskBlocked && strings.HasPrefix(deref(t.StateReason), "dependency_"))
		if !readyLike && !resumable {
			return nil, nil, invalid("task is %s; only ready, failed, stopped, paused or acknowledged unknown tasks can execute", t.State)
		}
		if resumable && !resume {
			return nil, nil, invalid("task is %s; resume must be requested explicitly", t.State)
		}
		if c.checkpointOccupied(*t) {
			return nil, nil, invalid("worktree is reserved by another task's checkpoint")
		}
		for _, o := range st.Tasks {
			if o.ID != t.ID && o.Worktree == t.Worktree && !slices.Contains(ids, o.ID) && o.State == model.TaskUnknown {
				return nil, nil, invalid("worktree is occupied by an unresolved task")
			}
		}
		if t.State == model.TaskUnknown || slices.ContainsFunc(st.Runs, func(r model.Run) bool { return r.TaskID == t.ID && r.State == model.RunUnknown }) {
			if !acknowledge {
				return nil, nil, invalid("task was interrupted with an unverified process; acknowledge after confirming it is gone")
			}
			for _, r := range st.Runs {
				if r.TaskID != t.ID || r.State != model.RunUnknown {
					continue
				}
				if r.PID != nil && (r.Host != c.host || processAlive(*r.PID)) {
					return nil, nil, invalid("a recorded process may still be alive or cannot be verified on this host; refusing to resume")
				}
				ack = append(ack, r.ID)
			}
		}
		for _, role := range model.Roles {
			if _, why := c.verifiedProfile(st, set, *t, role); why != "" {
				return nil, nil, invalid("task %s profile is not usable (%s)", role, why)
			}
		}
		if resumable {
			cp, e := c.savedCheckpoint(t.ID)
			if e != nil {
				return nil, nil, e
			}
			if cp != nil {
				p := pipelineOf(st, t.ID)
				maxFix := set.MaxFixRounds
				b, _, e := c.st.EffectiveBudget(t.ID)
				if e != nil {
					return nil, nil, e
				}
				maxFix = min(maxFix, b.MaxFixRounds)
				s := nextStep(p, maxFix)
				if e := c.verifyCheckpoint(st, *t, *cp, s); e != nil {
					return nil, nil, e
				}
				u, sec := budgetUse(st, t.ID)
				if b.HardTokenCap() && b.MaxTokens-u-futureStageReserve(*t, p, s) < 1 || float64(b.MaxWallSeconds)-sec < 1 {
					return nil, nil, invalid("checkpoint requires a new budget decision; original allowance is exhausted")
				}
				states[t.ID] = t.State
				continue
			}
			if t.State == model.TaskPaused {
				return nil, nil, invalid("paused task has no verified saved checkpoint")
			}
			f := inspect(t.Worktree)
			if f.Err != nil {
				return nil, nil, invalid("task worktree could not be inspected (%s); refusing to resume", inspectFailed)
			}
			if !f.Exists || f.Branch != deref(t.Branch) || !f.Clean {
				return nil, nil, invalid("task worktree is missing, on another branch or dirty (changes preserved)")
			}
			if exp := expectedHead(st, *t, pipelineOf(st, t.ID), f.Head); exp == "" || exp != f.Head {
				return nil, nil, invalid("worktree HEAD does not match the recorded candidate; refusing to resume")
			}
		}
		states[t.ID] = t.State
	}
	return ack, states, nil
}

// inspectFailed is the reason used when Git could not determine worktree facts (unknown, not a mismatch).
const inspectFailed = "worktree_inspection_failed"

type active struct {
	worktree  string
	projectID string
	providers []string
}

// depStatus reports a blocking reason or whether to wait. Delivered candidates are verified in Git.
func (c *Core) depStatus(st *model.State, t model.Task, pending []string, running map[string]active, selected []string) (string, bool) {
	wait := false
	for _, d := range t.Dependencies {
		if _, ok := running[d]; ok || slices.Contains(pending, d) {
			wait = true
			continue
		}
		dt := findTask(st, d)
		var dl *model.Delivery
		if dt != nil && dt.State == model.TaskDelivered {
			dl = deliveredCandidate(st, d)
		}
		if dl == nil {
			if slices.Contains(selected, d) {
				return "dependency_failed", false
			}
			return "dependency_not_delivered", false
		}
		if !gitOK(dt.Repository, "cat-file", "-e", dl.CandidateSha+"^{commit}") {
			return "dependency_candidate_missing", false
		}
		if dt.Repository == t.Repository {
			f := inspect(t.Worktree)
			if f.Err != nil {
				return inspectFailed, false
			}
			if !f.Exists || !isAncestor(t.Worktree, dl.CandidateSha, f.Head) {
				return "dependency_not_integrated", false
			}
		}
	}
	return "", wait
}

// queueMetrics records the scheduler queue wait (queued -> dispatched), which ends before any executor
// or model time. Without a measured queue the role started on intention: queuedAt = start, wait 0.
// Model and test seconds stay unknown (nil) unless a provider/tool reports them. FixRound is the known
// round: 0 for non-fix runs.
func queueMetrics(qw queueWait, start time.Time, fixRound int) *model.TimeMetrics {
	if qw.at.IsZero() || qw.until.Before(qw.at) {
		qw = queueWait{at: start, until: start}
	}
	q := qw.until.Sub(qw.at).Seconds()
	at := qw.at.UTC().Format(time.RFC3339Nano)
	fr := fixRound
	return &model.TimeMetrics{QueuedAt: &at, QueueSeconds: &q, FixRound: &fr}
}

func (c *Core) failTask(id, state, reason, role string) {
	_ = c.update(func(s *model.State) error {
		if t := findTask(s, id); t != nil {
			t.State, t.StateReason, t.UpdatedAt = state, sp(reason), now()
			t.ResumeRole = nil
			if role != "" {
				t.ResumeRole = sp(role)
			}
		}
		return nil
	})
}

// queueWait is a measured scheduler queue interval: queued at `at`, dispatched at `until`.
type queueWait struct{ at, until time.Time }

// runTask drives one task's roles back to back. The scheduler queue wait is attributed only to the
// first role run; later roles start right after the previous one in this goroutine (no queue).
func (c *Core) runTask(ctx context.Context, id, agent string, qw queueWait, operation *model.Operation) {
	first := true
	for i := 0; i < 20; i++ {
		if c.isLost() {
			return
		}
		st, err := c.st.Read()
		if err != nil {
			return
		}
		set, err := c.settings()
		if err != nil {
			c.failTask(id, model.TaskFailed, "settings_unreadable", "")
			return
		}
		t := findTask(st, id)
		if t == nil {
			return
		}
		if operation != nil {
			member := slices.IndexFunc(operation.Tasks, func(m model.OperationTask) bool { return m.TaskID == id })
			if member < 0 || taskContract(st, *t) != operation.Tasks[member].ContractDigest {
				c.failTask(id, model.TaskFailed, "dispatch_contract_changed", "")
				return
			}
			set.DefaultProfiles = nil
			set.MaxFixRounds = operation.FrozenFixRounds
		}
		p := pipelineOf(st, id)
		maxFix := set.MaxFixRounds
		if t.Budget != nil && t.Budget.MaxFixRounds < maxFix {
			maxFix = t.Budget.MaxFixRounds
		}
		if t.Origin == OriginDelegate && p.implemented {
			return // delegate mode: the verified developer candidate is final; review is external
		}
		s := nextStep(p, maxFix)
		if s.done == model.TaskDelivered {
			c.finalize(st, *t, p)
			return
		}
		if s.done != "" {
			c.failTask(id, s.done, s.reason, "")
			return
		}
		if ctx.Err() != nil || c.isClosed() {
			c.failTask(id, model.TaskStopped, "controller_stopped", s.role)
			return
		}
		intent := qw
		if !first {
			intent = queueWait{}
		}
		first = false
		if !c.runRole(ctx, st, set, *t, p, s, agent, intent) {
			return
		}
	}
	c.failTask(id, model.TaskFailed, "step_limit", "")
}

var roleContract = map[string]string{
	"implement": "Implement the goal within the scope below. Run relevant local checks. Finish with exactly one new local commit containing only in-scope paths, leaving the worktree clean. Report decision \"changed\".",
	"fix":       "Address ONLY the review findings listed below, within the scope. Run relevant local checks. Finish with exactly one new local commit containing only in-scope paths, leaving the worktree clean. Report decision \"changed\".",
	"check":     "Review the exact candidate commit against the goal, scope and acceptance criteria. Do not modify anything. Report verdict \"pass\" or \"changes_requested\" with concrete findings.",
	"recheck":   "Re-review the exact candidate commit (after polishing) against the goal, scope and acceptance criteria. Do not modify anything. Report verdict \"pass\" or \"changes_requested\" with concrete findings.",
	"polish":    "Make only small, safe improvements to paths already in scope. If you change anything, finish with exactly one new local commit and a clean worktree (decision \"changed\"). Otherwise leave HEAD and the worktree untouched and report decision \"no_change\".",
}

func brief(st *model.State, t model.Task, s step, candidate string, ctxRec model.Context, findings []executor.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nTask ID: %s\nRole: %s (%s)\n", t.Title, t.ID, s.role, s.purpose)
	if candidate != "" {
		fmt.Fprintf(&b, "Candidate commit: %s\n", candidate)
	}
	fmt.Fprintf(&b, "\n## Goal\n%s\n\n## Scope (only these repository-relative paths may change)\n", t.Goal)
	for _, x := range t.Scope {
		fmt.Fprintf(&b, "- %s\n", x)
	}
	b.WriteString("\n## Acceptance criteria\n")
	for _, x := range t.Acceptance {
		fmt.Fprintf(&b, "- %s\n", x)
	}
	fmt.Fprintf(&b, "\n## Frozen context (version %d, %s)\n%s\n", t.ContextRef.Version, t.ContextRef.Digest, ctxRec.Text)
	for _, d := range t.Dependencies {
		if dt, dl := findTask(st, d), deliveredCandidate(st, d); dt != nil && dl != nil {
			fmt.Fprintf(&b, "\nDependency candidate (reference only, never merged by the controller): %s (%s): %s\n", dt.Title, dt.ID, dl.CandidateSha)
		}
	}
	if len(findings) > 0 {
		b.WriteString("\n## Review findings to address\n")
		for _, f := range findings {
			fmt.Fprintf(&b, "- %s: %s %s\n", f.ID, f.Summary, f.Path)
		}
	}
	fmt.Fprintf(&b, "\n## Role contract\n%s\n", roleContract[s.purpose])
	out := b.String()
	if len(out) > 250<<10 {
		out = out[:250<<10]
	}
	return out
}

// frozenOK re-derives the frozen context digest and scope.
func frozenOK(st *model.State, t model.Task) (model.Context, string) {
	if t.Budget != nil && !t.Budget.ValidMode() {
		return model.Context{}, "budget_mode_invalid"
	}
	ctxRec := findContext(st, t.ContextRef)
	if ctxRec == nil || ContextDigest(ctxRec.Text, ctxRec.Sources) != t.ContextRef.Digest {
		return model.Context{}, "context_changed"
	}
	if sc, err := NormalizeScope(t.Scope); err != nil || !slices.Equal(sc, t.Scope) {
		return model.Context{}, "scope_changed"
	}
	return *ctxRec, ""
}

// runRole runs one role. qw is the measured scheduler queue interval for the task's first role; a zero
// value means no queue preceded this role (it starts on intention), recorded as a known 0 wait.
func (c *Core) runRole(ctx context.Context, st *model.State, set model.Settings, t model.Task, p pipeline, s step, agent string, qw queueWait) bool {
	if c.checkpointOccupied(t) {
		c.failTask(t.ID, model.TaskBlocked, "worktree_reserved_checkpoint", s.role)
		return false
	}
	prof, why := c.verifiedProfile(st, set, t, s.role)
	if why != "" {
		c.failTask(t.ID, model.TaskFailed, why, s.role)
		return false
	}
	ctxRec, why := frozenOK(st, t)
	if why != "" {
		c.failTask(t.ID, model.TaskFailed, why, s.role)
		return false
	}
	usedTok, usedSec := budgetUse(st, t.ID)
	budget, budgetRevision, budgetLookupErr := c.st.EffectiveBudget(t.ID)
	if budgetLookupErr != nil {
		c.failTask(t.ID, model.TaskUnknown, "budget_authorization_unverifiable", s.role)
		return false
	}
	remTok, remSec := budget.MaxTokens-usedTok-futureStageReserve(t, p, s), int64(float64(budget.MaxWallSeconds)-usedSec)
	if !budget.HardTokenCap() {
		remTok = int64(prof.Limits.MaxTokens)
	}
	resumeCP, checkpointErr := c.savedCheckpoint(t.ID)
	if checkpointErr != nil {
		c.failTask(t.ID, model.TaskFailed, "checkpoint_unreadable", s.role)
		return false
	}
	if remTok < 1 || remSec < 1 {
		reason := "budget_tokens"
		if remTok >= 1 {
			reason = "budget_time"
		}
		state := model.TaskStopped
		if resumeCP != nil {
			state = model.TaskPaused
		}
		c.failTask(t.ID, state, reason, s.role)
		return false
	}
	capTok, capSec := min(int64(prof.Limits.MaxTokens), remTok), min(int64(prof.Limits.MaxWallSeconds), remSec)
	f := inspect(t.Worktree)
	base := ""
	roleBase := ""
	pre := ""
	switch {
	case f.Err != nil:
		pre = inspectFailed // unknown facts are never reported as a branch/dirty/candidate change
	case !f.Exists:
		pre = "worktree_missing"
	case f.Branch != deref(t.Branch):
		pre = "branch_changed"
	case !f.Clean && resumeCP == nil:
		pre = "dirty_worktree_preserved"
	default:
		base = expectedHead(st, t, p, f.Head)
		if resumeCP != nil {
			base = resumeCP.HeadSHA
		}
		if base == "" {
			pre = "candidate_missing"
			if !p.implemented {
				pre = "baseline_changed_requires_prepare"
			}
		} else if f.Head != base {
			pre = "candidate_mismatch"
		}
	}
	if pre != "" {
		c.failTask(t.ID, model.TaskFailed, pre, s.role)
		return false
	}
	if resumeCP != nil && c.verifyCheckpoint(st, t, *resumeCP, s) != nil {
		c.failTask(t.ID, model.TaskPaused, "checkpoint_mismatch", s.role)
		return false
	}
	roleBase = base
	if resumeCP != nil {
		roleBase = resumeCP.BaselineSHA
	}
	ss, fresh, sessionErr := c.selectSession(st, t, prof, s.role, base)
	if sessionErr != nil {
		c.failTask(t.ID, model.TaskUnknown, "session_unverifiable", s.role)
		return false
	}
	if resumeCP != nil && (ss == nil || ss.ID != resumeCP.SessionID || ss.FileDigest != resumeCP.SessionDigest) {
		c.failTask(t.ID, model.TaskPaused, "checkpoint_session_mismatch", s.role)
		return false
	}
	var findings []executor.Finding
	if s.purpose == "fix" {
		for _, r := range st.Reviews {
			if r.RunID == p.lastReviewRunID {
				_ = json.Unmarshal(r.Findings, &findings)
			}
		}
	}
	runsDir := filepath.Join(c.st.DataDir(), "runs")
	runID := newUUID()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runsDir, 0o700); err != nil || platform.Mkdir(runDir, 0o700) != nil {
		c.failTask(t.ID, model.TaskFailed, "scratch_unavailable", s.role)
		return false
	}
	defer os.RemoveAll(runDir)

	lctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	l := &lane{cancel: cancel}
	if ss != nil {
		if x, ok := c.reg[execName(prof)].(executor.StatefulExecutor); ok && x.Capabilities().GracefulWrapUp {
			l.controls = make(chan executor.RunControl, 1)
			l.controlSession = ss.ID
			l.canPause = x.Capabilities().GracefulPause
			l.canFollowUp = x.Capabilities().QueuedFollowUp
		}
	}
	c.mu.Lock()
	c.lanes[runID] = l
	if c.lost {
		cancel(ErrLeaseLost)
	} else if c.closed {
		cancel(errControllerStopped)
	}
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.lanes, runID); c.mu.Unlock() }()

	sm := runSummary{Purpose: s.purpose, Limits: limits{MaxTokens: capTok, MaxWallSeconds: capSec}, BudgetRevision: budgetRevision}
	var resumeRecords []model.Checkpoint
	if resumeCP != nil {
		sm.CheckpointID = resumeCP.ID
		resumeRecords = append(resumeRecords, *resumeCP)
	}
	if s.purpose == "fix" {
		sm.FixRound = p.fixRounds + 1
	}
	startT := time.Now()
	ts := startT.UTC().Format(time.RFC3339Nano)
	ref := t.ContextRef
	run := model.Run{ID: runID, AgentID: agent, TaskID: t.ID, Role: s.role, ProfileID: prof.ID, Executor: execName(prof),
		ModelSnapshot: &model.ModelSnapshot{ProfileID: prof.ID, Provider: prof.Provider, Model: prof.Model}, ContextRef: &ref,
		State: model.RunStarting, StartedAt: ts, UpdatedAt: ts, Events: []model.RunEvent{}, Summary: mustJSON(sm), Origin: model.OriginNative,
		Metrics: queueMetrics(qw, startT, sm.FixRound)}
	pushEvent(&run, "state", "starting")
	err := c.startRoleRecord(ss, runID, fresh, func(st *model.State) error {
		tt := findTask(st, t.ID)
		if tt == nil {
			return invalid("task vanished")
		}
		if ss != nil && taskContract(st, *tt) != ss.ContractDigest {
			return invalid("task changed before session claim")
		}
		st.Runs = append(st.Runs, run)
		tt.State, tt.StateReason, tt.ResumeRole, tt.UpdatedAt = s.taskState, nil, sp(s.role), ts
		return nil
	}, resumeRecords...)
	if err != nil {
		if !c.isLost() {
			c.failTask(t.ID, model.TaskFailed, "persistence_failed", s.role)
		}
		return false
	}

	guard := func(fn func(*model.Run)) {
		if lctx.Err() != nil && errors.Is(context.Cause(lctx), ErrLeaseLost) {
			return
		}
		if err := c.update(func(st *model.State) error {
			if r := findRun(st, runID); r != nil {
				fn(r)
			}
			return nil
		}); err != nil {
			cancel(ErrLeaseLost)
		}
	}
	var wrapRequested atomic.Bool
	onEvent := func(e model.RunEvent) {
		if (e.Type == "budget" || e.Type == "control") && e.Summary == "wrap_up_requested" {
			wrapRequested.Store(true)
		}
		guard(func(r *model.Run) { pushEvent(r, e.Type, e.Summary) })
	}
	onStart := func(pr executor.Process) {
		guard(func(r *model.Run) {
			if pr.PID > 0 {
				pid := pr.PID
				r.PID = &pid
			}
			r.Host, r.ProcessStartedAt = pr.Host, pr.StartedAt.UTC().Format(time.RFC3339Nano)
			if r.State == model.RunStarting {
				r.State = model.RunRunning
			}
			pushEvent(r, "state", "running")
		})
	}
	req := executor.Request{RunID: runID, Profile: prof, Context: ctxRec, Role: s.role, Worktree: t.Worktree,
		TaskBrief: brief(st, t, s, map[bool]string{true: base}[p.implemented], ctxRec, findings), ReportPath: filepath.Join(runDir, "report.json"),
		ExpectedSHA: base, ContextDigest: t.ContextRef.Digest, RemainingTokens: capTok, RemainingWall: time.Duration(capSec) * time.Second, Env: c.opts.Env}
	if ss != nil {
		binding := c.sessionBinding(*ss, t.Worktree)
		req.Session = &binding
	}
	if l.controls != nil {
		req.Controls = &executor.ControlBinding{Messages: l.controls, Authority: &controlAuthority{c: c, runID: runID}}
	}
	if resumeCP != nil {
		req.RoleBaselineSHA = roleBase
		req.Checkpoint = &checkpoint.Binding{Digest: resumeCP.FileDigest, Scope: slices.Clone(t.Scope)}
		req.TaskBrief += "\nContinue the exact verified local checkpoint and this session's history. Preserve existing staged and unstaged work, inspect only what the task needs, and finish the original role contract. Past checks and incomplete work are not delivery. The original task allowance still applies."
		if roleBase != base {
			req.TaskBrief += "\nThe saved HEAD " + base + " is a provisional commit. The original role baseline is " + roleBase + ". Finish with exactly one in-scope commit from that original baseline; amend the provisional commit if changes are needed. Do not create a second commit. A report on the unchanged saved HEAD may complete the changed role; it is not a no-change decision relative to the original role baseline."
		}
	}
	if budget.StageReserves != nil {
		req.WrapUpTokens = min(budget.StageReserves.WrapUpTokens, capTok-1)
		req.WrapUpBefore = time.Duration(min(budget.StageReserves.WrapUpSeconds, capSec-1)) * time.Second
	}
	var authority *requestAuthority
	var budgetErr error
	if ss != nil {
		if x, ok := c.reg[execName(prof)].(interface{ Capabilities() executor.Capabilities }); ok && x.Capabilities().RequestBudgetGate {
			authority, budgetErr = c.openRequestBudget(t, prof, ss, runID, capTok, capSec)
			req.Budget = authority
			if authority != nil {
				req.WrapUp = authority.wrapUp
			}
		}
	}
	started := time.Now()
	var xr executor.Result
	var xerr error
	if budgetErr == nil {
		xr, xerr = c.reg[execName(prof)].Execute(lctx, req, onEvent, onStart)
	} else {
		xerr = &executor.Error{Category: executor.CatBudgetUnknown, Message: "request authority could not be established"}
		xr.Session = &executor.SessionOutcome{ID: ss.ID, ProviderID: ss.ProviderID, Digest: ss.FileDigest, Confirmed: true}
	}
	wall := time.Since(started).Seconds()
	if err := c.closeControls(runID, l); err != nil {
		// Receipt persistence is required before claiming a verified completion.
		xr.Category = executor.CatSessionUnknown
		if xr.Session != nil {
			xr.Session.Confirmed = false
		}
	}
	cause := context.Cause(lctx)
	if lctx.Err() == nil {
		cause = nil
	}
	if c.isLost() || errors.Is(cause, ErrLeaseLost) {
		return false // fenced out: state stays active and is projected unknown
	}
	budgetKnown, budgetOverrun := budgetErr == nil, false
	if authority != nil {
		outcome, err := c.closeRequestBudget(runID)
		budgetKnown, budgetOverrun = err == nil && outcome.Confirmed, outcome.Overrun
		if err == nil {
			xr.Usage = outcome.Usage
		}
		if c.isLost() {
			return false
		}
	}
	if xr.Category == executor.CatBudgetUnknown {
		budgetKnown = false
	}

	// Git facts before the final transaction.
	after := inspect(t.Worktree)
	sessionKnown := ss == nil || xr.Session != nil && xr.Session.Confirmed && xr.Session.ID == ss.ID && xr.Session.ProviderID == ss.ProviderID && sessionDigestRE.MatchString(xr.Session.Digest)
	if ss != nil {
		ss.State, ss.ActiveRunID, ss.UpdatedAt = model.SessionUnknown, nil, now()
		if sessionKnown && after.Err == nil && after.Exists && after.Branch == deref(t.Branch) && shaRE.MatchString(after.Head) {
			ss.State, ss.FileDigest, ss.LastSHA = model.SessionIdle, xr.Session.Digest, after.Head
		} else {
			sessionKnown = false
		}
	}
	cat, reason := "", ""
	var changed []string
	stopped := false
	if xerr != nil || xr.Outcome != model.RunSucceeded {
		var ee *executor.Error
		cat = xr.Category
		if errors.As(xerr, &ee) && cat == "" {
			cat = ee.Category
		}
		if cat == "" {
			cat = "executor_error"
		}
		reason = cat
		switch {
		case errors.Is(cause, errStopRequested):
			stopped, reason = true, "stop_requested"
		case cause != nil:
			stopped, reason = true, "controller_stopped"
		case cat == executor.CatPauseRequested:
			stopped, reason = true, "pause_requested"
		case cat == executor.CatTokenLimit:
			stopped = true
			if float64(capTok) < prof.Limits.MaxTokens {
				reason = "budget_tokens"
			}
		case cat == executor.CatWallTimeout:
			stopped = true
			if float64(capSec) < prof.Limits.MaxWallSeconds {
				reason = "budget_time"
			}
		case xr.Outcome == model.RunStopped || cat == executor.CatCanceled:
			stopped = true
		}
	} else {
		changed, cat = verifyRoleFrom(s.role, t, base, roleBase, after, xr)
		reason = cat
	}
	if !sessionKnown {
		cat, reason, stopped = executor.CatSessionUnknown, "session_unverifiable", false
	}
	if sessionKnown && budgetOverrun {
		cat, reason, stopped = executor.CatTokenLimit, "budget_request_overrun", true
	}
	if !budgetKnown {
		cat, reason, stopped = executor.CatBudgetUnknown, "request_budget_unverifiable", false
	}
	rep := xr.Report
	if cat == "" {
		if _, why := frozenOK(st, t); why != "" {
			cat, reason = why, why
		}
	}
	var saved []model.Checkpoint
	var checkpointFailure string
	partial := cat == executor.CatPauseRequested || cat == executor.CatTokenLimit || cat == executor.CatWallTimeout || wrapRequested.Load() && slices.Contains([]string{executor.CatReportMissing, executor.CatDirty, executor.CatNoCommit}, cat)
	if partial && cause == nil && ss != nil && sessionKnown && budgetKnown && !budgetOverrun && xr.CheckpointSafe && xr.Usage.Tokens.Total != nil && s.role != "reviewer" {
		if cp, e := c.captureCheckpoint(st, t, *ss, runID, roleBase, s); e == nil {
			saved = append(saved, *cp)
			stopped = true
		} else {
			checkpointFailure = checkpoint.FailureCode(e)
		}
	}
	t2 := now()
	var stopReqs []string
	c.mu.Lock()
	stopReqs = slices.Clone(l.stops)
	c.mu.Unlock()
	ok := cat == ""
	var finalState string
	settle := func(st *model.State) error {
		r, tt := findRun(st, runID), findTask(st, t.ID)
		if r == nil || tt == nil {
			return invalid("run vanished")
		}
		if ok {
			if _, why := frozenOK(st, *tt); why != "" {
				ok, cat, reason = false, why, why
			}
		}
		r.EndedAt = sp(t2)
		if r.Metrics == nil {
			r.Metrics = &model.TimeMetrics{}
		}
		r.Metrics.WallSeconds = &wall
		if xr.Process != nil {
			u := xr.Usage
			if u.Validate() == nil {
				r.Usage = &u
			}
			if xr.Process.PID > 0 && r.PID == nil {
				pid := xr.Process.PID
				r.PID = &pid
			}
		}
		sm := summaryOf(*r)
		sm.BaselineSha = base
		if !ok {
			if checkpointFailure != "" {
				pushEvent(r, "checkpoint", "rejected_"+checkpointFailure)
			}
			r.State = model.RunFailed
			tt.State = model.TaskFailed
			if stopped {
				r.State, tt.State = model.RunStopped, model.TaskStopped
			}
			if len(saved) == 1 {
				tt.State = model.TaskPaused
				sm.CheckpointID = saved[0].ID
				pushEvent(r, "checkpoint", "saved")
			}
			if !sessionKnown || !budgetKnown {
				r.State, tt.State = model.RunUnknown, model.TaskUnknown
			}
			sm.Outcome, sm.ErrorCategory = r.State, cat
			tt.StateReason, tt.ResumeRole, tt.UpdatedAt = sp(reason), sp(s.role), t2
			pushEvent(r, "state", r.State)
			r.Summary, finalState = mustJSON(sm), r.State
			return nil
		}
		r.State, finalState = model.RunSucceeded, model.RunSucceeded
		sm.Outcome, sm.ResultSha, sm.ChangedPaths = "success", after.Head, changed
		if len(sm.ChangedPaths) > 200 {
			sm.ChangedPaths = sm.ChangedPaths[:200]
		}
		if s.purpose == "implement" {
			sm.StartSha = roleBase
		}
		sm.Verdict, sm.Decision = rep.Verdict, rep.Decision
		sm.Report = &reportView{Summary: rep.Summary, Checks: rep.Checks, KnownGaps: rep.KnownGaps, Findings: rep.Findings}
		r.Summary = mustJSON(sm)
		pushEvent(r, "state", "succeeded")
		if s.role != "reviewer" {
			tt.CandidateSha = sp(after.Head)
		}
		if s.purpose == "implement" {
			st.Deliveries = append(st.Deliveries, delivery(*tt, after.Head, []string{runID}, checks(false, rep), rep.KnownGaps, "first", t2))
			tt.State = model.TaskFirstDelivery
			if tt.Origin == OriginDelegate {
				tt.StateReason, tt.ResumeRole = sp(DelegateCandidate), nil
			}
		}
		if s.role == "reviewer" {
			findings := rep.Findings
			if findings == nil {
				findings = []executor.Finding{}
			}
			st.Reviews = append(st.Reviews, model.Review{ID: newUUID(), TaskID: tt.ID, RunID: runID, CandidateSha: after.Head,
				ContextDigest: deref(rep.ContextDigest), Verdict: rep.Verdict, Findings: mustJSON(findings), Checks: mustJSON(rep.Checks), CreatedAt: t2})
			if rep.Verdict == "pass" && s.purpose == "check" {
				st.Deliveries = append(st.Deliveries, delivery(*tt, after.Head, succeededRuns(st, tt.ID), checks(true, rep), rep.KnownGaps, "final_candidate", t2))
				tt.State = model.TaskFinalCandidate
			}
		}
		tt.UpdatedAt = t2
		return nil
	}
	if ss != nil && ok && sessionKnown && budgetKnown && !budgetOverrun && xr.CheckpointSafe && xr.Usage.Tokens.Total != nil {
		err = c.stageAndFinishCompletion(runID, *ss, xr.Process, settle)
	} else {
		err = c.finishRoleRecord(ss, runID, settle, saved...)
	}
	if err != nil {
		if !c.isLost() {
			c.failTask(t.ID, model.TaskFailed, "persistence_failed", s.role)
		}
		return false
	}
	for _, rq := range stopReqs {
		_ = c.finishStop(rq, finalState)
	}
	return ok
}

// verifyRole checks the exact executor report against independently gathered Git facts.
func verifyRole(role string, t model.Task, base string, f wtFacts, xr executor.Result) ([]string, string) {
	return verifyRoleFrom(role, t, base, base, f, xr)
}

// Execution starts at the saved HEAD; scope and the one-commit rule cover the
// whole role, including a provisional commit made before a verified checkpoint.
func verifyRoleFrom(role string, t model.Task, base, roleBase string, f wtFacts, xr executor.Result) ([]string, string) {
	rep := xr.Report
	if rep == nil {
		return nil, executor.CatReportMissing
	}
	if model.CheckFreeForm(toAny(rep), "report") != nil {
		return nil, executor.CatReportInvalid
	}
	if f.Err != nil {
		return nil, inspectFailed
	}
	if !f.Exists {
		return nil, "worktree_missing"
	}
	if f.Branch != deref(t.Branch) {
		return nil, executor.CatBranchChanged
	}
	if !f.Clean {
		return nil, executor.CatDirty
	}
	if xr.BaselineSHA != base {
		return nil, "baseline_mismatch"
	}
	if rep.CandidateSHA != f.Head || xr.ResultSHA != f.Head || rep.ContextDigest == nil || *rep.ContextDigest != t.ContextRef.Digest {
		return nil, executor.CatReportStale
	}
	changed := changedPaths(t.Worktree, roleBase, f.Head)
	if changed == nil {
		return nil, "git_unverifiable"
	}
	switch role {
	case "reviewer":
		if f.Head != roleBase || len(changed) > 0 {
			return nil, executor.CatReviewerMutation
		}
		if rep.Verdict != "pass" && rep.Verdict != "changes_requested" {
			return nil, executor.CatReportInvalid
		}
		return changed, ""
	case "polisher":
		if rep.Decision == "no_change" {
			if f.Head != roleBase {
				return nil, executor.CatDecisionMismatch
			}
			return changed, ""
		}
		if rep.Decision != "changed" {
			return nil, executor.CatDecisionMismatch
		}
	default:
		if rep.Decision != "changed" {
			return nil, executor.CatDecisionMismatch
		}
	}
	if f.Head == roleBase || !isAncestor(t.Worktree, roleBase, f.Head) {
		return nil, executor.CatNoCommit
	}
	if commitCount(t.Worktree, roleBase, f.Head) != 1 {
		return nil, "commit_count"
	}
	if slices.ContainsFunc(changed, func(p string) bool { return !inScope(p, t.Scope) }) {
		return nil, "scope_violation"
	}
	return changed, ""
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func checks(review bool, rep *executor.Report) json.RawMessage {
	out := []map[string]string{{"name": "git_head_matches_candidate", "status": "pass"}, {"name": "worktree_clean", "status": "pass"},
		{"name": "changed_paths_in_scope", "status": "pass"}, {"name": "context_digest_matches", "status": "pass"}}
	if review {
		out = append(out, map[string]string{"name": "review_verdict", "status": "pass"})
	}
	if rep != nil {
		for _, c := range rep.Checks {
			out = append(out, map[string]string{"name": "reported", "status": "reported", "command": c.Command, "result": c.Result})
		}
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return mustJSON(out)
}

func delivery(t model.Task, sha string, runIDs []string, chk json.RawMessage, gaps []string, state, ts string) model.Delivery {
	if gaps == nil {
		gaps = []string{}
	}
	return model.Delivery{ID: newUUID(), TaskID: t.ID, ContextRef: t.ContextRef, CandidateSha: sha, Repository: t.Repository,
		RunIDs: runIDs, Checks: chk, KnownGaps: gaps, State: state, CreatedAt: ts, UpdatedAt: ts}
}

func succeededRuns(st *model.State, taskID string) []string {
	ids := []string{}
	for _, r := range st.Runs {
		if r.TaskID == taskID && r.State == model.RunSucceeded {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) > 50 {
		ids = ids[len(ids)-50:]
	}
	return ids
}

// finalize verifies the fresh final review on the exact current SHA/digest and records delivery.
func (c *Core) finalize(st *model.State, t model.Task, p pipeline) {
	f := inspect(t.Worktree)
	cand := deref(t.CandidateSha)
	var review *model.Review
	for i := range st.Reviews {
		if st.Reviews[i].RunID == p.lastReviewRunID {
			review = &st.Reviews[i]
		}
	}
	bad := ""
	_, frozen := frozenOK(st, t)
	switch {
	case f.Err != nil:
		bad = inspectFailed
	case !f.Exists:
		bad = "worktree_missing"
	case f.Branch != deref(t.Branch) || !f.Clean:
		bad = "worktree_state"
	case !shaRE.MatchString(cand) || f.Head != cand:
		bad = "candidate_mismatch"
	case review == nil || review.Verdict != "pass" || review.CandidateSha != cand:
		bad = "review_not_on_candidate"
	case frozen != "" || review.ContextDigest != t.ContextRef.Digest:
		bad = "context_not_fresh"
	case p.startSha == "" || !isAncestor(t.Worktree, deref(t.BaselineSha), cand) || !isAncestor(t.Worktree, p.startSha, cand):
		bad = "baseline_not_ancestor"
	default:
		ch := changedPaths(t.Worktree, p.startSha, cand)
		if ch == nil {
			bad = "git_unverifiable"
		} else if slices.ContainsFunc(ch, func(x string) bool { return !inScope(x, t.Scope) }) {
			bad = "scope_violation"
		}
	}
	if bad != "" {
		c.failTask(t.ID, model.TaskFailed, "delivery_verification_failed:"+bad, "reviewer")
		return
	}
	_ = c.update(func(s *model.State) error {
		tt := findTask(s, t.ID)
		if tt == nil || deref(tt.CandidateSha) != cand {
			return invalid("task changed during delivery")
		}
		var gaps []string
		for _, r := range s.Runs {
			if r.ID == review.RunID {
				if sm := summaryOf(r); sm.Report != nil {
					gaps = sm.Report.KnownGaps
				}
			}
		}
		ts := now()
		rep := &executor.Report{}
		_ = json.Unmarshal(review.Checks, &rep.Checks)
		s.Deliveries = append(s.Deliveries, delivery(*tt, cand, succeededRuns(s, tt.ID), checks(true, rep), gaps, "delivered", ts))
		tt.State, tt.StateReason, tt.ResumeRole, tt.UpdatedAt = model.TaskDelivered, nil, nil, ts
		return nil
	})
}
