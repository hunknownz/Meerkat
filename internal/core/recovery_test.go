//go:build unix

package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
	"github.com/hunknownz/Meerkat/internal/store"
)

type completionFake struct {
	*statefulFake
	process *executor.Process
}

func (f *completionFake) Execute(ctx context.Context, r executor.Request, event func(model.RunEvent), start func(executor.Process)) (executor.Result, error) {
	onStart := start
	if f.process != nil {
		onStart = func(executor.Process) { start(*f.process) }
	}
	v, err := f.statefulFake.Execute(ctx, r, event, onStart)
	v.CheckpointSafe = true
	if f.process != nil {
		v.Process = f.process
	}
	return v, err
}
func completionEnv(t *testing.T) (*env, *completionFake) {
	e := setup(t)
	f := &completionFake{statefulFake: useSessions(t, e)}
	e.c.reg["fake"] = f
	return e, f
}
func interruptCompletion(t *testing.T, e *env) model.Task {
	return interruptRoleCompletion(t, e, "developer")
}
func interruptRoleCompletion(t *testing.T, e *env, role string, run ...func(string)) model.Task {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(e.data, "meerkat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fixture_interrupt BEFORE UPDATE OF state ON execution_sessions
 WHEN NEW.state='idle' AND json_extract(OLD.payload,'$.role')='` + role + `' BEGIN SELECT RAISE(ABORT,'fixture settlement interruption'); END;`); err != nil {
		t.Fatal(err)
	}
	task := e.prepare(e.worktree("completion-interrupted"), nil)
	if len(run) == 0 {
		e.exec(task.ID)
	} else {
		run[0](task.ID)
	}
	runs := e.state().Runs
	if len(runs) == 0 || runs[len(runs)-1].Role != role {
		t.Fatal("wrong interruption phase", len(runs))
	}
	v, err := e.st.Completion(runs[len(runs)-1].ID)
	if err != nil || v.State != "pending" {
		t.Fatal(v.State, err)
	}
	e.c.Close()
	if _, err = db.Exec("DROP TRIGGER fixture_interrupt"); err != nil {
		t.Fatal(err)
	}
	c, err := New(e.st, e.c.reg, Options{Poll: 10 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	if findTask(e.state(), task.ID).State != model.TaskUnknown {
		t.Fatal("interruption automatically recovered")
	}
	return task
}

func TestRecoveryAsyncOperationNeedsSeparateDispatch(t *testing.T) {
	e, f := completionEnv(t)
	var old model.DispatchReceipt
	task := interruptRoleCompletion(t, e, "developer", func(id string) {
		old = dispatch(t, e.c, id)
		settled(t, e.c, old.Operation.ID)
	})
	prior, err := e.c.Operation(old.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.ApplyRecovery(recoveryInput(t, e, task)); err != nil {
		t.Fatal(err)
	}
	if o, err := e.c.Operation(old.Operation.ID); err != nil || !reflect.DeepEqual(o, prior) || len(f.reqs) != 1 {
		t.Fatal("recovery changed or replayed original operation", o, err)
	}
	next, err := e.c.Dispatch(model.DispatchRequest{RequestID: newUUID(), TaskIDs: []string{task.ID}, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if o := settled(t, e.c, next.Operation.ID); o.Tasks[0].TaskState != model.TaskDelivered || len(f.reqs) != 4 {
		t.Fatal("new continuation failed or repeated developer", o)
	}
}
func recoveryInput(t *testing.T, e *env, task model.Task) model.RecoveryInput {
	t.Helper()
	v, err := e.c.InspectRecovery(task.ID)
	if err != nil || v.Status != "verified" || v.Proposal == nil {
		t.Fatal(v, err)
	}
	return model.RecoveryInput{Proposal: *v.Proposal, RequestID: newUUID(), AuthorizationRef: "EXPLICIT_PRIVATE_RECOVERY_AUTH", Apply: true}
}
func TestRecoverySettlementWindowPreservesResultWithoutReplay(t *testing.T) {
	e, f := completionEnv(t)
	task := interruptCompletion(t, e)
	in := recoveryInput(t, e, task)
	if len(f.reqs) != 1 {
		t.Fatal("inspection executed an agent")
	}
	backup := filepath.Join(e.root, "pending.db")
	if err := e.st.Backup(backup); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(e.root, "pending-restored")
	if err := store.Restore(backup, dst); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := restored.ProposeRecovery(task.ID, in.Proposal.RunID); err != nil || !reflect.DeepEqual(p, in.Proposal) {
		t.Fatal("pending restore changed evidence", p, err)
	}
	v, err := restored.Completion(in.Proposal.RunID)
	if err != nil {
		t.Fatal(err)
	}
	ss := v.Evidence.AfterSession
	if snap, err := f.InspectSession(executor.SessionBinding{ID: ss.ID, ProviderID: ss.ProviderID, File: filepath.Join(dst, ss.FileRef), Worktree: task.Worktree}); err != nil || snap.Digest != ss.FileDigest {
		t.Fatal("restored history changed", err)
	}
	restored.Close()
	bad := in
	bad.Apply = false
	if _, err := e.c.ApplyRecovery(bad); err == nil {
		t.Fatal("implicit recovery accepted")
	}
	rc, err := e.c.ApplyRecovery(in)
	if err != nil {
		t.Fatal(err)
	}
	st := e.state()
	if len(f.reqs) != 1 || st.Runs[0].State != model.RunSucceeded || findTask(st, task.ID).State != model.TaskStopped || len(st.Deliveries) != 1 {
		t.Fatal("recovery executed or duplicated a result")
	}
	if st.Runs[0].Usage.Tokens.Total == nil || *st.Runs[0].Usage.Tokens.Total != 100 || st.Runs[0].Usage.EstimatedCostUsd != nil {
		t.Fatal("usage or unknown fee changed")
	}
	if _, err := e.c.Execute(context.Background(), []string{task.ID}, false, false); err == nil {
		t.Fatal("implicit continuation accepted")
	}
	r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false)
	if err != nil || r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r, err)
	}
	n := 0
	for _, r := range f.reqs {
		if r.Role == "developer" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("completed developer replayed")
	}
	if got, err := e.c.ApplyRecovery(in); err != nil || !reflect.DeepEqual(got, rc) {
		t.Fatal("receipt lost after continuation", got, err)
	}
	bad = in
	bad.AuthorizationRef = "different permission"
	if _, err := e.c.ApplyRecovery(bad); err == nil {
		t.Fatal("request identity replaced")
	}
	snap, err := e.c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snap)
	for _, private := range []string{in.AuthorizationRef, in.Proposal.CompletionDigest, in.Proposal.EvidenceDigest, "private fixture history"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private evidence leaked")
		}
	}
	if err := e.st.Backup(filepath.Join(e.root, "recovered.db")); err != nil {
		t.Fatal(err)
	}
	dst = filepath.Join(e.root, "recovered-restored")
	if err := store.Restore(filepath.Join(e.root, "recovered.db"), dst); err != nil {
		t.Fatal(err)
	}
	restored, err = store.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	decision, err := restored.RecoveryDecision(in.RequestID)
	if err != nil || !reflect.DeepEqual(recoveryReceipt(decision), rc) {
		t.Fatal("restore lost recovery receipt", err)
	}
	restoredState, err := restored.Read()
	if err != nil || !reflect.DeepEqual(restoredState.Runs, e.state().Runs) || !reflect.DeepEqual(restoredState.Deliveries, e.state().Deliveries) {
		t.Fatal("restore changed recovered results", err)
	}
}

func TestRecoveryReviewerAndPolisherDoNotRepeatSettledStep(t *testing.T) {
	for _, tc := range []struct {
		name, role     string
		change, reject bool
	}{
		{"review pass", "reviewer", false, false},
		{"review requests fix", "reviewer", false, true},
		{"polish unchanged", "polisher", false, false},
		{"polish changed", "polisher", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f := completionEnv(t)
			f.polish = tc.change
			if tc.reject {
				f.verdicts = []string{"changes_requested", "pass", "pass"}
			}
			task := interruptRoleCompletion(t, e, tc.role)
			prefix := append([]executor.Request{}, f.reqs...)
			in := recoveryInput(t, e, task)
			if _, err := e.c.ApplyRecovery(in); err != nil {
				t.Fatal(err)
			}
			if len(f.reqs) != len(prefix) || findTask(e.state(), task.ID).State != model.TaskStopped {
				t.Fatal("recovery resumed an executor")
			}
			res, err := e.c.Execute(context.Background(), []string{task.ID}, true, false)
			if err != nil || res.Tasks[0].State != model.TaskDelivered {
				t.Fatal("continuation failed", res, err)
			}
			want := "developer,reviewer,polisher,reviewer"
			if tc.reject {
				want = "developer,reviewer,developer,reviewer,polisher,reviewer"
			}
			if strings.Join(f.roles, ",") != want {
				t.Fatal("settled role repeated", f.roles)
			}
			st := e.state()
			seen := map[string]bool{}
			for _, r := range st.Reviews {
				if seen[r.RunID] || r.Verdict == "pass" && r.RunID == st.Runs[len(st.Runs)-1].ID && r.CandidateSha != deref(findTask(st, task.ID).CandidateSha) {
					t.Fatal("review duplicated or bound to wrong SHA")
				}
				seen[r.RunID] = true
			}
			for i, req := range prefix {
				if f.reqs[i].RunID != req.RunID {
					t.Fatal("replaced completed run")
				}
			}
		})
	}
}

func TestRecoveryUnknownRequestAccountingStaysBlocked(t *testing.T) {
	e, _ := completionEnv(t)
	task := interruptCompletion(t, e)
	in := recoveryInput(t, e, task)
	v, err := e.st.Completion(in.Proposal.RunID)
	if err != nil {
		t.Fatal(err)
	}
	ss := v.Evidence.BeforeSession
	p := budget.Policy{RunID: in.Proposal.RunID, TaskID: task.ID, SessionID: ss.ID, ProfileID: ss.ProfileID, ProfileDigest: ss.ProfileDigest,
		ContractDigest: ss.ContractDigest, Provider: "fixture", Model: "fixture", Version: budget.PolicyVersion, Deadline: now(), TaskTokens: 1000, RunTokens: 1000, TaskRequests: 10, State: "unknown"}
	raw, _ := json.Marshal(p)
	db, err := sql.Open("sqlite", filepath.Join(e.data, "meerkat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO budget_runs(run_id,task_id,session_id,state,payload) VALUES(?,?,?,?,?)", p.RunID, p.TaskID, p.SessionID, p.State, raw); err != nil {
		t.Fatal(err)
	}
	got, err := e.c.InspectRecovery(task.ID)
	if err != nil || got.Status != "blocked" || got.Proposal != nil || got.Checks[len(got.Checks)-1].Reason != "request_accounting_or_state_unresolved" {
		t.Fatal("unknown accounting treated as settled", got, err)
	}
	if _, err := e.c.ApplyRecovery(in); err == nil {
		t.Fatal("unknown accounting recovered")
	}
}
func TestRecoveryRejectsMissingChangedAndUncertainEvidence(t *testing.T) {
	for _, kind := range []string{"missing", "file", "session", "profile", "usage", "stale", "clone"} {
		t.Run(kind, func(t *testing.T) {
			e, _ := completionEnv(t)
			task := interruptCompletion(t, e)
			in := recoveryInput(t, e, task)
			switch kind {
			case "missing":
				db, _ := sql.Open("sqlite", filepath.Join(e.data, "meerkat.db"))
				_, err := db.Exec("DELETE FROM run_completions")
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "file":
				os.WriteFile(filepath.Join(task.Worktree, "src/a.txt"), []byte("external modification"), 0o644)
			case "session":
				ss, _ := e.st.SessionForRun(in.Proposal.RunID)
				p := filepath.Join(e.data, ss.FileRef)
				file, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
				file.WriteString("{\"type\":\"external\"}\n")
				file.Close()
			case "profile":
				p := e.profiles["developer"]
				raw, _ := os.ReadFile(p)
				os.WriteFile(p, []byte(strings.Replace(string(raw), "m-1", "m-2", 1)), 0o600)
			case "usage":
				e.c.update(func(st *model.State) error {
					st.Runs[0].Usage = &model.Usage{UsageCompleteness: model.UsageUnknown}
					return nil
				})
			case "stale":
				e.c.update(func(st *model.State) error { findTask(st, task.ID).ResumeRole = sp("reviewer"); return nil })
			case "clone":
				clone := filepath.Join(e.root, "replacement-clone")
				sh(t, e.root, "clone", "--local", e.repo, clone)
				v, err := e.st.Completion(in.Proposal.RunID)
				if err != nil {
					t.Fatal(err)
				}
				sh(t, clone, "checkout", "-B", deref(task.Branch), v.Evidence.AfterSession.LastSHA)
				if err := os.Rename(task.Worktree, task.Worktree+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(clone, task.Worktree); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.c.ApplyRecovery(in); err == nil {
				t.Fatal("invalid evidence recovered")
			}
			if len(e.fx.reqs) != 1 {
				t.Fatal("recovery called executor")
			}
		})
	}
}
func TestRecoveryConcurrentDecisionAndLiveProcessRefusal(t *testing.T) {
	t.Run("one winner", func(t *testing.T) {
		e, _ := completionEnv(t)
		task := interruptCompletion(t, e)
		in := recoveryInput(t, e, task)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v := in
				v.RequestID = newUUID()
				_, err := e.c.ApplyRecovery(v)
				results <- err
			}()
		}
		wg.Wait()
		close(results)
		n := 0
		for err := range results {
			if err == nil {
				n++
			}
		}
		if n != 1 {
			t.Fatal("completion recovered twice", n)
		}
	})
	t.Run("live group", func(t *testing.T) {
		e, f := completionEnv(t)
		cmd := exec.Command("sleep", "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cmd.Process.Kill(); cmd.Wait() }()
		host, _ := os.Hostname()
		f.process = &executor.Process{PID: cmd.Process.Pid, PGID: cmd.Process.Pid, Host: host, StartedAt: time.Now().UTC(), Executor: "fake"}
		task := interruptCompletion(t, e)
		v, err := e.c.InspectRecovery(task.ID)
		if err != nil || v.Status != "blocked" || v.Proposal != nil {
			t.Fatal("live group recovered", v, err)
		}
		if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
			t.Fatal("inspection signaled fixture process")
		}
	})
}

func TestRecoveryNormalCompletionAndCorruptBackup(t *testing.T) {
	e, _ := completionEnv(t)
	task := e.prepare(e.worktree("normal-completion"), nil)
	if r := e.exec(task.ID); r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r)
	}
	for _, r := range e.state().Runs {
		v, err := e.st.Completion(r.ID)
		if err != nil || v.State != "settled" {
			t.Fatal("normal completion missing", v.State, err)
		}
	}
	if v, err := e.c.InspectRecovery(task.ID); err != nil || v.Status != "blocked" {
		t.Fatal("completed task recoverable", v, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(e.data, "meerkat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE run_completions SET digest=?", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Backup(filepath.Join(e.root, "corrupt-completion.db")); err == nil {
		t.Fatal("corrupt completion backed up")
	}
}
