package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

type checkpointFake struct {
	*statefulFake
	calls        int
	unsafe       bool
	unknownUsage bool
	outOfScope   bool
	pauseRole    string
	paused       bool
	resumed      bool
	committed    bool
	cleanCommit  bool
	finish       string
	provisional  string
	roleBaseline string
}

func (f *checkpointFake) Execute(ctx context.Context, req executor.Request, onEvent func(model.RunEvent), onStart func(executor.Process)) (executor.Result, error) {
	f.calls++
	role := f.pauseRole
	if role == "" {
		role = "developer"
	}
	if f.paused || req.Role != role {
		if f.paused && !f.resumed && req.Checkpoint == nil {
			return executor.Result{}, invalid("missing resume binding")
		}
		if req.Checkpoint != nil && checkpoint.Verify(req.Worktree, *req.Checkpoint) != nil {
			return executor.Result{}, invalid("dirty binding changed")
		}
		if req.Checkpoint != nil {
			f.resumed = true
			if f.committed {
				return f.finishCommitted(req, onStart)
			}
		}
		return f.statefulFake.Execute(ctx, req, onEvent, onStart)
	}
	f.paused = true
	f.roleBaseline = req.ExpectedSHA
	p := filepath.Join(req.Worktree, "src", "partial.txt")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if f.outOfScope {
		p = filepath.Join(req.Worktree, "outside.txt")
	}
	if e := os.WriteFile(p, []byte("staged work"), 0o644); e != nil {
		return executor.Result{}, e
	}
	if _, e := git(req.Worktree, "add", "--", strings.TrimPrefix(p, req.Worktree+string(os.PathSeparator))); e != nil {
		return executor.Result{}, e
	}
	os.WriteFile(p, []byte("PRIVATE CHECKPOINT WORK 工作进度"), 0o644)
	os.WriteFile(filepath.Join(req.Worktree, "src", "new file.txt"), []byte("new work"), 0o644)
	if f.committed {
		if _, e := git(req.Worktree, "add", "-A"); e != nil {
			return executor.Result{}, e
		}
		if _, e := git(req.Worktree, "commit", "-qm", "provisional work"); e != nil {
			return executor.Result{}, e
		}
		f.provisional, _ = git(req.Worktree, "rev-parse", "HEAD")
		if !f.cleanCommit {
			os.WriteFile(p, []byte("retained dirty progress"), 0o644)
		}
	}
	onEvent(model.RunEvent{Type: "budget", Summary: "wrap_up_requested"})
	pr := executor.Process{Executor: "fake", Host: "fixture", StartedAt: time.Now()}
	onStart(pr)
	total, zero := int64(100), int64(0)
	u := model.Usage{Tokens: model.TokenCounts{Input: &total, Output: &zero, CacheRead: &zero, CacheWrite: &zero, Total: &total}, UsageCompleteness: model.UsageComplete}
	if f.unknownUsage {
		u.Tokens.Total = nil
		u.UsageCompleteness = model.UsageUnknown
	}
	snap, e := f.InspectSession(*req.Session)
	if e != nil {
		return executor.Result{}, e
	}
	r := executor.Result{RunID: req.RunID, Process: &pr, Outcome: model.RunStopped, Category: executor.CatTokenLimit, BaselineSHA: req.ExpectedSHA, ResultSHA: req.ExpectedSHA, Usage: u,
		Session: &executor.SessionOutcome{ID: req.Session.ID, ProviderID: snap.ProviderID, Digest: snap.Digest, Confirmed: true}, CheckpointSafe: !f.unsafe}
	return r, &executor.Error{Category: executor.CatTokenLimit}
}

func (f *checkpointFake) finishCommitted(req executor.Request, onStart func(executor.Process)) (executor.Result, error) {
	if req.ExpectedSHA != f.provisional || req.RoleBaselineSHA != f.roleBaseline || req.Session == nil {
		return executor.Result{}, invalid("wrong committed checkpoint execution HEAD")
	}
	snap, e := f.InspectSession(*req.Session)
	if e != nil || snap.Digest != req.Session.Digest {
		return executor.Result{}, invalid("wrong continuation history")
	}
	if f.finish == "amend" || !f.cleanCommit {
		if _, e := git(req.Worktree, "add", "-A"); e != nil {
			return executor.Result{}, e
		}
		if _, e := git(req.Worktree, "commit", "--amend", "-qm", "finished work"); e != nil {
			return executor.Result{}, e
		}
	}
	if f.finish == "extra" {
		if e := commit(req.Worktree, "src/extra.txt", "second commit"); e != nil {
			return executor.Result{}, e
		}
	}
	if f.finish == "scope" {
		os.WriteFile(filepath.Join(req.Worktree, "outside.txt"), []byte("outside"), 0o644)
		git(req.Worktree, "add", "-A")
		git(req.Worktree, "commit", "--amend", "-qm", "outside scope")
	}
	head, e := git(req.Worktree, "rev-parse", "HEAD")
	if e != nil {
		return executor.Result{}, e
	}
	pr := executor.Process{Executor: "fake", Host: "fixture", StartedAt: time.Now()}
	onStart(pr)
	total := int64(100)
	r := executor.Result{RunID: req.RunID, Process: &pr, Outcome: model.RunSucceeded, BaselineSHA: req.ExpectedSHA, ResultSHA: head,
		Usage:   model.Usage{Tokens: model.TokenCounts{Total: &total}, UsageCompleteness: model.UsagePartial},
		Session: &executor.SessionOutcome{ID: req.Session.ID, ProviderID: snap.ProviderID, Digest: snap.Digest, Confirmed: true}, CheckpointSafe: true}
	if f.finish == "pause" {
		f.provisional = head
		r.Outcome, r.Category = model.RunStopped, executor.CatTokenLimit
		return r, &executor.Error{Category: executor.CatTokenLimit}
	}
	decision := "changed"
	if f.finish == "no_change" {
		decision = "no_change"
	}
	digest := req.ContextDigest
	r.Report = &executor.Report{CandidateSHA: head, ContextDigest: &digest, Decision: decision, Summary: "completed original role", Checks: []executor.Check{}, KnownGaps: []string{}}
	return r, nil
}
func checkpointEnv(t *testing.T) (*env, *checkpointFake) {
	e := setup(t)
	e.c.Close()
	f := &checkpointFake{statefulFake: &statefulFake{fakeExec: e.fx}}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: 10 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	return e, f
}
func pausedTask(t *testing.T, e *env) model.Task {
	t.Helper()
	task := e.prepare(e.worktree("checkpoint"), func(m map[string]any) {
		m["budget"] = map[string]any{"maxTokens": 1050, "maxWallSeconds": 1800, "maxFixRounds": 1}
	})
	r := e.exec(task.ID)
	if r.Tasks[0].State != model.TaskPaused {
		t.Fatalf("checkpoint was not saved: %+v", r)
	}
	return task
}
func TestCheckpointResumePreservesDirtyWorkHistoryAndOriginalBudget(t *testing.T) {
	e, f := checkpointEnv(t)
	task := pausedTask(t, e)
	if _, err := e.c.Execute(context.Background(), []string{task.ID}, false, false); err == nil {
		t.Fatal("implicit resume accepted")
	}
	before, _ := e.st.CheckpointsForTask(task.ID)
	if len(before) != 1 || before[0].State != "saved" || before[0].FileCount != 2 {
		t.Fatal(before)
	}
	public, err := e.c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	for _, private := range []string{"PRIVATE CHECKPOINT WORK", before[0].FileRef, before[0].FileDigest, before[0].SessionDigest} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private checkpoint data leaked")
		}
	}
	r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false)
	if err != nil || r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r, err)
	}
	after, _ := e.st.CheckpointsForTask(task.ID)
	if after[0].State != "consumed" || after[0].ResumedRunID == nil {
		t.Fatal(after)
	}
	resumed, _ := e.st.SessionForRun(*after[0].ResumedRunID)
	if resumed.ID != before[0].SessionID {
		t.Fatal("session replaced")
	}
	if got := sh(t, task.Worktree, "show", "HEAD:src/partial.txt"); got != "PRIVATE CHECKPOINT WORK 工作进度" {
		t.Fatal("saved work replaced", got)
	}
	if e.fx.reqs[0].RemainingTokens != 950 {
		t.Fatal("past tokens were reset", e.fx.reqs[0].RemainingTokens)
	}
	st := e.state()
	tokens, _ := budgetUse(st, task.ID)
	if tokens != 500 {
		t.Fatal("usage mismatch", tokens)
	}
	if _, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err == nil {
		t.Fatal("consumed checkpoint replayed")
	}
	if f.calls != 5 {
		t.Fatal(f.calls)
	}
}
func TestCheckpointResumeRejectsChangedEvidenceBeforeExecution(t *testing.T) {
	for _, kind := range []string{"file", "extra", "missing", "index", "head", "branch", "context", "profile", "session", "archive"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			task := pausedTask(t, e)
			switch kind {
			case "file":
				os.WriteFile(filepath.Join(task.Worktree, "src", "partial.txt"), []byte("external edit"), 0o644)
			case "extra":
				os.WriteFile(filepath.Join(task.Worktree, "src", "extra.txt"), []byte("extra"), 0o644)
			case "missing":
				os.Remove(filepath.Join(task.Worktree, "src", "new file.txt"))
			case "index":
				sh(t, task.Worktree, "reset", "-q", "HEAD", "--", "src/partial.txt")
			case "head":
				sh(t, task.Worktree, "commit", "-qm", "unverified commit")
			case "branch":
				sh(t, task.Worktree, "switch", "-c", "different")
			case "context":
				e.c.update(func(st *model.State) error { findTask(st, task.ID).Goal = "changed"; return nil })
			case "profile":
				b, _ := os.ReadFile(e.profiles["developer"])
				os.WriteFile(e.profiles["developer"], []byte(strings.ReplaceAll(string(b), "m-1", "m-2")), 0o600)
			case "session":
				cp, _ := e.st.CheckpointsForTask(task.ID)
				ss, _ := e.st.SessionForRun(cp[0].RunID)
				file, _ := os.OpenFile(filepath.Join(e.data, ss.FileRef), os.O_APPEND|os.O_WRONLY, 0)
				file.WriteString("corrupt history\n")
				file.Close()
			case "archive":
				cp, _ := e.st.CheckpointsForTask(task.ID)
				os.WriteFile(filepath.Join(e.data, cp[0].FileRef), []byte("{}"), 0o600)
			}
			if _, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err == nil {
				t.Fatal("changed evidence accepted")
			}
			if f.calls != 1 {
				t.Fatal("executor started before verification", f.calls)
			}
			if findTask(e.state(), task.ID).State != model.TaskPaused {
				t.Fatal("rejection destroyed paused state")
			}
		})
	}
}
func TestCheckpointUnsafeOrUnknownWorkNeverBecomesResumable(t *testing.T) {
	for _, kind := range []string{"unsafe-tool", "unknown-usage", "scope"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			f.unsafe = kind == "unsafe-tool"
			f.unknownUsage = kind == "unknown-usage"
			f.outOfScope = kind == "scope"
			task := e.prepare(e.worktree("unsafe"), nil)
			r := e.exec(task.ID)
			if r.Tasks[0].State == model.TaskPaused {
				t.Fatal("uncertain work became paused")
			}
			cp, err := e.st.CheckpointsForTask(task.ID)
			if err != nil || len(cp) != 0 {
				t.Fatal(cp, err)
			}
			if _, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err == nil {
				t.Fatal("dirty unknown work resumed")
			}
		})
	}
}
func TestCheckpointOwnsWorktreeAndSurvivesRestartAndBackup(t *testing.T) {
	e, f := checkpointEnv(t)
	wt := e.worktree("shared")
	task := e.prepare(wt, nil)
	other := e.prepare(wt, nil)
	if r := e.exec(task.ID); r.Tasks[0].State != model.TaskPaused {
		t.Fatal(r)
	}
	if _, err := e.c.Execute(context.Background(), []string{other.ID}, false, false); err == nil {
		t.Fatal("other task adopted checkpoint")
	}
	e.c.Close()
	backup := filepath.Join(e.root, "checkpoint.db")
	if err := e.st.Backup(backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(e.root, "restored")
	if err := store.Restore(backup, restored); err != nil {
		t.Fatal(err)
	}
	rst, err := store.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer rst.Close()
	c, err := New(rst, Registry{"fake": f}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.Execute(context.Background(), []string{task.ID}, true, false)
	if err != nil || r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r, err)
	}
	consumed := filepath.Join(e.root, "consumed.db")
	if err := rst.Backup(consumed); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateBackup(consumed); err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(consumed, filepath.Join(e.root, "restored-consumed")); err != nil {
		t.Fatal(err)
	}
	cp, _ := e.st.CheckpointsForTask(task.ID)
	os.WriteFile(filepath.Join(e.data, cp[0].FileRef), []byte("corrupt"), 0o600)
	bad := filepath.Join(e.root, "bad.db")
	if err := e.st.Backup(bad); err == nil {
		t.Fatal("corrupt file backed up")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("partial backup remains")
	}
}

func TestCheckpointPolisherContinuationInvalidatesOldReview(t *testing.T) {
	e, f := checkpointEnv(t)
	f.pauseRole = "polisher"
	task := e.prepare(e.worktree("polisher-checkpoint"), nil)
	if r := e.exec(task.ID); r.Tasks[0].State != model.TaskPaused {
		t.Fatal(r)
	}
	before := e.state()
	old := deref(findTask(before, task.ID).CandidateSha)
	if len(before.Reviews) != 1 || before.Reviews[0].CandidateSha != old {
		t.Fatal("initial review missing")
	}
	e.fx.polish = true
	r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false)
	if err != nil || r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r, err)
	}
	after := e.state()
	last := after.Reviews[len(after.Reviews)-1]
	if len(after.Reviews) != 2 || last.CandidateSha == old || last.CandidateSha != deref(findTask(after, task.ID).CandidateSha) {
		t.Fatal("polish reused an obsolete review")
	}
}

func TestCheckpointClaimCrashIsUnknownAndNeverReplayed(t *testing.T) {
	e, f := checkpointEnv(t)
	task := pausedTask(t, e)
	cp, _ := e.c.savedCheckpoint(task.ID)
	ss, err := e.st.SessionForRun(cp.RunID)
	if err != nil {
		t.Fatal(err)
	}
	rid, at := newUUID(), now()
	ref := task.ContextRef
	r := model.Run{ID: rid, AgentID: "fixture", TaskID: task.ID, Role: ss.Role, ProfileID: ss.ProfileID, Executor: ss.Executor, ContextRef: &ref, State: model.RunStarting, StartedAt: at, UpdatedAt: at, Events: []model.RunEvent{}, Summary: mustJSON(runSummary{Purpose: "implement", Limits: limits{MaxTokens: 950, MaxWallSeconds: 60}})}
	if err := e.c.startRoleRecord(&ss, rid, false, func(st *model.State) error {
		st.Runs = append(st.Runs, r)
		findTask(st, task.ID).State = model.TaskImplementing
		return nil
	}, *cp); err != nil {
		t.Fatal(err)
	}
	e.c.Close()
	c, err := New(e.st, Registry{"fake": f}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	st := e.state()
	if findTask(st, task.ID).State != model.TaskUnknown || findRun(st, rid).State != model.RunUnknown {
		t.Fatal("uncertain claim became known")
	}
	all, _ := e.st.CheckpointsForTask(task.ID)
	if all[0].State != "consumed" {
		t.Fatal("checkpoint rearmed after crash")
	}
	if _, err := c.Execute(context.Background(), []string{task.ID}, true, true); err == nil {
		t.Fatal("uncertain history resumed")
	}
	if f.calls != 1 {
		t.Fatal("crashed run replayed")
	}
}

func TestCheckpointResumeCannotExpandOriginalAllowance(t *testing.T) {
	e, f := checkpointEnv(t)
	task := e.prepare(e.worktree("exhausted"), func(m map[string]any) {
		m["budget"] = map[string]any{"maxTokens": 100, "maxWallSeconds": 1800, "maxFixRounds": 1}
	})
	if r := e.exec(task.ID); r.Tasks[0].State != model.TaskPaused {
		t.Fatal(r)
	}
	if _, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err == nil || !strings.Contains(err.Error(), "allowance is exhausted") {
		t.Fatal("budget expansion accepted", err)
	}
	if f.calls != 1 {
		t.Fatal("budget exhaustion submitted a prompt")
	}
}

func TestCheckpointCommittedProgressResumesAgainstOriginalRoleBaseline(t *testing.T) {
	for _, kind := range []string{"clean", "dirty-amend", "polisher"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			f.committed, f.cleanCommit = true, kind != "dirty-amend"
			if kind == "polisher" {
				f.pauseRole = "polisher"
			}
			task := pausedTask(t, e)
			cp, err := e.c.savedCheckpoint(task.ID)
			if err != nil || cp == nil || cp.HeadSHA == cp.BaselineSHA || cp.HeadSHA != f.provisional {
				t.Fatal("committed progress was not saved", cp, err)
			}
			before := e.state()
			candidate := deref(findTask(before, task.ID).CandidateSha)
			if kind != "polisher" && (candidate != "" || len(before.Deliveries) != 0 || len(before.Reviews) != 0) {
				t.Fatal("provisional commit was promoted to delivery")
			}
			// Verify that a backup and restart preserve the distinct SHA bindings.
			e.c.Close()
			backup := filepath.Join(e.root, "committed-checkpoint.db")
			if err := e.st.Backup(backup); err != nil {
				t.Fatal(err)
			}
			restored := filepath.Join(e.root, "restored-committed")
			if err := store.Restore(backup, restored); err != nil {
				t.Fatal(err)
			}
			rst, err := store.Open(restored)
			if err != nil {
				t.Fatal(err)
			}
			defer rst.Close()
			c, err := New(rst, Registry{"fake": f}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r, err := c.Execute(context.Background(), []string{task.ID}, true, false)
			if err != nil || r.Tasks[0].State != model.TaskDelivered {
				t.Fatal(r, err)
			}
			st, err := rst.Read()
			if err != nil {
				t.Fatal(err)
			}
			final := findTask(st, task.ID)
			if commitCount(task.Worktree, cp.BaselineSHA, deref(final.CandidateSha)) != 1 {
				t.Fatal("role commit count was reset to the resumed HEAD")
			}
			if kind == "dirty-amend" && deref(final.CandidateSha) == cp.HeadSHA {
				t.Fatal("dirty progress was not included in amended delivery")
			}
			if kind == "clean" && deref(final.CandidateSha) != cp.HeadSHA {
				t.Fatal("unchanged provisional HEAD required an unnecessary commit")
			}
			if kind == "polisher" && (len(st.Reviews) != 2 || st.Reviews[1].CandidateSha == candidate || st.Reviews[1].CandidateSha != deref(final.CandidateSha)) {
				t.Fatal("committed polish reused the old review")
			}
			if kind != "polisher" && pipelineOf(st, task.ID).startSha != cp.BaselineSHA {
				t.Fatal("final scope lost the original implementation baseline")
			}
			all, _ := rst.CheckpointsForTask(task.ID)
			ss, err := rst.SessionForRun(*all[0].ResumedRunID)
			if err != nil || ss.ID != cp.SessionID || all[0].State != "consumed" {
				t.Fatal("continuation replaced history", ss, err)
			}
			if used, _ := budgetUse(st, task.ID); used < 200 || final.Budget.MaxTokens != task.Budget.MaxTokens {
				t.Fatal("continuation reset frozen usage or budget")
			}
		})
	}
}

func TestCheckpointCommittedContinuationStillRejectsScopeAndExtraCommit(t *testing.T) {
	for _, kind := range []string{"extra", "scope", "no_change"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			f.committed, f.cleanCommit, f.finish = true, true, kind
			if kind == "no_change" {
				f.pauseRole = "polisher"
			}
			task := pausedTask(t, e)
			r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false)
			if err != nil || r.Tasks[0].State != model.TaskFailed {
				t.Fatal("invalid resumed role accepted", r, err)
			}
			reason := map[string]string{"extra": "commit_count", "scope": "scope_violation", "no_change": executor.CatDecisionMismatch}[kind]
			if deref(r.Tasks[0].StateReason) != reason {
				t.Fatal("wrong rejection", r.Tasks[0])
			}
		})
	}
}

func TestCheckpointCommittedRepeatedPauseRetainsBaselineAndExactHead(t *testing.T) {
	e, f := checkpointEnv(t)
	f.committed, f.cleanCommit = true, true
	task := pausedTask(t, e)
	first, _ := e.c.savedCheckpoint(task.ID)
	f.finish = "pause"
	if r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err != nil || r.Tasks[0].State != model.TaskPaused {
		t.Fatal(r, err)
	}
	second, _ := e.c.savedCheckpoint(task.ID)
	if first.ID == second.ID || first.BaselineSHA != second.BaselineSHA || first.HeadSHA != second.HeadSHA {
		t.Fatal("repeated pause lost original role bindings", first, second)
	}
	f.finish = ""
	if r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err != nil || r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r, err)
	}
}

func TestCheckpointCommittedUnsafeOrUnknownWorkIsNotResumable(t *testing.T) {
	for _, kind := range []string{"unsafe-tool", "unknown-usage"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			f.committed, f.cleanCommit = true, true
			f.unsafe, f.unknownUsage = kind == "unsafe-tool", kind == "unknown-usage"
			task := e.prepare(e.worktree("committed-uncertain"), nil)
			if r := e.exec(task.ID); r.Tasks[0].State == model.TaskPaused {
				t.Fatal("uncertain committed work became resumable")
			}
			cp, err := e.st.CheckpointsForTask(task.ID)
			if err != nil || len(cp) != 0 {
				t.Fatal(cp, err)
			}
		})
	}
}

func TestCheckpointCommittedCaptureRejectsOutsideMultipleAndUnrelatedHeads(t *testing.T) {
	for _, kind := range []string{"scope", "multiple", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			f.committed, f.cleanCommit = true, true
			if kind == "scope" {
				f.outOfScope = true
				task := e.prepare(e.worktree("committed-scope"), nil)
				if r := e.exec(task.ID); r.Tasks[0].State == model.TaskPaused {
					t.Fatal("out-of-scope committed progress saved")
				}
				return
			}
			task := pausedTask(t, e)
			cp, _ := e.c.savedCheckpoint(task.ID)
			ss, _ := e.st.SessionForRun(cp.RunID)
			if kind == "multiple" {
				if err := commit(task.Worktree, "src/another.txt", "second commit"); err != nil {
					t.Fatal(err)
				}
			} else {
				sh(t, task.Worktree, "checkout", "--orphan", "unrelated")
				sh(t, task.Worktree, "commit", "-qm", "unrelated root")
				sh(t, task.Worktree, "branch", "-M", deref(task.Branch))
			}
			ss.LastSHA = sh(t, task.Worktree, "rev-parse", "HEAD")
			if _, err := e.c.captureCheckpoint(e.state(), task, ss, cp.RunID, cp.BaselineSHA, step{role: "developer", purpose: "implement"}); err == nil {
				t.Fatal("invalid committed checkpoint accepted")
			}
		})
	}
}
